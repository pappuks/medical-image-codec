// mic_parallel.c — parallel single-image decompression for the PICS strip format.
//
// Design
// ------
// A PICS blob divides one image into N horizontal strips, each an independent
// MIC-compressed block (two-state or four-state FSE + RLE + delta).  Because
// strips share no data they can be decoded in any order on any number of cores.
//
// This file implements:
//  1. PICS header parsing (read width, height, strip count, offset table).
//  2. A pthread worker pool that decompresses strips concurrently and writes
//     each strip's pixels directly into the correct row range of pixels_out.
//  3. A semaphore-free, bounded-concurrency scheduler: at most max_threads
//     pthreads are live at once; new threads are launched as slots free up.
//
// The actual per-strip decompression is delegated to mic_decompress_four_state
// (or the SIMD variant) from mic_decompress_c.c — no codec logic lives here.
//
// Portability
// -----------
// C99 + POSIX pthreads.  Compiles unmodified on Linux (AMD64 / ARM64), macOS,
// and most BSDs.  The SIMD path is gated by __x86_64__ / __aarch64__ macros
// inside mic_decompress_c.c; this file is platform-agnostic.
//
// Build (AMD64 with AVX2):
//   gcc -O3 -msse2 -mavx2 -pthread mic_decompress_c.c mic_parallel.c
// Build (ARM64):
//   gcc -O3 -pthread mic_decompress_c.c mic_parallel.c

#include "mic_parallel.h"
#include "mic_decompress_c.h"

#include <stdlib.h>
#include <string.h>
#include <pthread.h>

// ---------------------------------------------------------------------------
// PICS header layout (matches parallelstrips.go)
// ---------------------------------------------------------------------------
//
//   Bytes  0-3:  "PICS"
//   Bytes  4-7:  width       (uint32 LE)
//   Bytes  8-11: height      (uint32 LE)
//   Bytes 12-15: num_strips  (uint32 LE)
//   Bytes 16-19: strip_h     (uint32 LE) — rows per strip (last may be shorter)
//   Bytes 20+:   offset table: num_strips × [offset_u32 LE, length_u32 LE]
//   After table: concatenated compressed strip blobs

static inline uint32_t read_u32le(const uint8_t *p) {
    return (uint32_t)p[0] |
           ((uint32_t)p[1] << 8) |
           ((uint32_t)p[2] << 16) |
           ((uint32_t)p[3] << 24);
}

// ---------------------------------------------------------------------------
// Per-strip work item
// ---------------------------------------------------------------------------
typedef int (*decomp_fn_t)(const uint8_t *, size_t, uint16_t *, int, int);

typedef struct {
    // Input
    const uint8_t *strip_data;   // pointer to compressed strip blob
    size_t         strip_len;    // byte length of that blob
    uint16_t      *row_out;      // output: pixels_out + y0 * width
    int            width;
    int            strip_height; // rows in this strip (last strip may differ)
    decomp_fn_t    decomp;       // which inner decoder to call

    // Output
    int            error;        // 0 = success, non-zero = decomp returned error
} strip_job_t;

// ---------------------------------------------------------------------------
// Thread entry point: decompress one strip, write into row_out.
// ---------------------------------------------------------------------------
static void *strip_worker(void *arg) {
    strip_job_t *j = (strip_job_t *)arg;
    j->error = j->decomp(j->strip_data, j->strip_len,
                         j->row_out, j->width, j->strip_height);
    return NULL;
}

// ---------------------------------------------------------------------------
// Core implementation
// ---------------------------------------------------------------------------
static int decompress_parallel_impl(const uint8_t *compressed, size_t compressed_len,
                                    uint16_t *pixels_out, int width, int height,
                                    int max_threads, decomp_fn_t decomp_fn) {
    // --- Parse PICS header ---
    if (compressed_len < PICS_HEADER_BASE) return -1;
    if (memcmp(compressed, PICS_MAGIC, 4) != 0) return -2;

    uint32_t hdr_width    = read_u32le(compressed + 4);
    uint32_t hdr_height   = read_u32le(compressed + 8);
    uint32_t num_strips   = read_u32le(compressed + 12);
    uint32_t strip_h      = read_u32le(compressed + 16);

    if ((int)hdr_width != width || (int)hdr_height != height) return -3;
    if (num_strips == 0 || strip_h == 0) return -4;

    size_t header_size = PICS_HEADER_BASE + (size_t)num_strips * 8;
    if (compressed_len < header_size) return -5;

    // Build job array
    strip_job_t *jobs = (strip_job_t *)calloc(num_strips, sizeof(strip_job_t));
    if (!jobs) return -6;

    for (uint32_t s = 0; s < num_strips; s++) {
        const uint8_t *tbl = compressed + PICS_HEADER_BASE + s * 8;
        uint32_t offset = read_u32le(tbl);
        uint32_t len    = read_u32le(tbl + 4);

        size_t start = header_size + offset;
        size_t end   = start + len;
        if (end > compressed_len) { free(jobs); return -7; }

        uint32_t y0 = s * strip_h;
        uint32_t y1 = y0 + strip_h;
        if (y1 > (uint32_t)height) y1 = (uint32_t)height;

        jobs[s].strip_data   = compressed + start;
        jobs[s].strip_len    = len;
        jobs[s].row_out      = pixels_out + (size_t)y0 * width;
        jobs[s].width        = width;
        jobs[s].strip_height = (int)(y1 - y0);
        jobs[s].decomp       = decomp_fn;
        jobs[s].error        = 0;
    }

    // --- Dispatch strips to pthreads ---
    // Strategy: bounded concurrency — keep at most max_threads live pthreads.
    // We iterate over strips in order; whenever we have max_threads active we
    // join the oldest outstanding thread before launching the next one.
    // This uses O(max_threads) memory for thread handles with no extra sync.

    if (max_threads <= 0 || (uint32_t)max_threads > num_strips) {
        max_threads = (int)num_strips;
    }

    pthread_t *threads = (pthread_t *)calloc(max_threads, sizeof(pthread_t));
    if (!threads) { free(jobs); return -8; }

    // Round-robin slot index; we reclaim slot (i % max_threads) before reuse.
    int rc = 0;
    uint32_t launched = 0;

    for (uint32_t s = 0; s < num_strips && rc == 0; s++) {
        int slot = (int)(s % (uint32_t)max_threads);

        // If we've already filled this slot, wait for the old thread first.
        if ((int)launched >= max_threads) {
            pthread_join(threads[slot], NULL);
            // Check the error from the thread that just finished.
            uint32_t prev = s - (uint32_t)max_threads;
            if (jobs[prev].error != 0) rc = jobs[prev].error;
        }

        if (rc == 0) {
            if (pthread_create(&threads[slot], NULL, strip_worker, &jobs[s]) != 0) {
                rc = -9;
            } else {
                launched++;
            }
        }
    }

    // Join all remaining live threads.
    uint32_t remaining = launched < (uint32_t)max_threads ? launched
                                                           : (uint32_t)max_threads;
    for (uint32_t t = 0; t < remaining; t++) {
        uint32_t slot_start = (num_strips < (uint32_t)max_threads)
                              ? 0
                              : (uint32_t)(num_strips % (uint32_t)max_threads);
        uint32_t slot = (slot_start + t) % (uint32_t)max_threads;
        pthread_join(threads[slot], NULL);

        // Determine which job ran in this slot last.
        uint32_t last_s = (uint32_t)max_threads <= num_strips
                          ? num_strips - (uint32_t)max_threads + ((slot - (num_strips % (uint32_t)max_threads) + (uint32_t)max_threads) % (uint32_t)max_threads)
                          : slot;
        if (last_s < num_strips && jobs[last_s].error != 0 && rc == 0)
            rc = jobs[last_s].error;
    }

    free(threads);
    free(jobs);
    return rc;
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// Choose the best available inner decoder for this platform.
// On AMD64: four-state SIMD (SSE2 RLE/delta fill + scalar FSE).
// On ARM64 and others: scalar four-state (NEON acceleration is not yet wired
//   into mic_decompress_four_state_simd; use the scalar path which is already
//   fast due to the 4-state FSE ILP).
static decomp_fn_t best_decomp_fn(void) {
#if defined(__x86_64__) || defined(_M_X64)
    return mic_decompress_four_state_simd;
#else
    return mic_decompress_four_state;
#endif
}

int mic_decompress_parallel(const uint8_t *compressed, size_t compressed_len,
                            uint16_t *pixels_out, int width, int height,
                            int max_threads) {
    return decompress_parallel_impl(compressed, compressed_len,
                                    pixels_out, width, height,
                                    max_threads, best_decomp_fn());
}

int mic_decompress_parallel_scalar(const uint8_t *compressed, size_t compressed_len,
                                   uint16_t *pixels_out, int width, int height,
                                   int max_threads) {
    return decompress_parallel_impl(compressed, compressed_len,
                                    pixels_out, width, height,
                                    max_threads, mic_decompress_four_state);
}
