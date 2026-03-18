// mic_parallel.h — parallel single-image decompression via PICS strip format.
//
// A PICS blob (produced by CompressParallelStrips in Go) contains N independent
// compressed strips.  mic_decompress_parallel dispatches each strip to a
// pthread worker and writes the decoded pixels directly into the caller-supplied
// output buffer.  All strips run concurrently, so on an N-core machine the wall
// time for decompression shrinks by ~N× compared to sequential single-strip
// decoding.
//
// Architecture notes
// ------------------
// The implementation is architecture-agnostic C99 + POSIX threads; the SIMD
// acceleration lives inside each strip's call to mic_decompress_four_state_simd
// (SSE2/AVX2 on AMD64, scalar on ARM64 — see mic_decompress_c.h for details).
// No architecture-specific code is needed here: goroutines in Go and pthreads in
// C both provide the same thread-level parallelism on x86-64, ARM64, and RISC-V.
//
// To compile with SIMD acceleration on AMD64:
//   gcc -O3 -msse2 -mavx2 -lpthread mic_decompress_c.c mic_parallel.c -o …
// On ARM64 (NEON is always available):
//   gcc -O3 -lpthread mic_decompress_c.c mic_parallel.c -o …

#ifndef MIC_PARALLEL_H
#define MIC_PARALLEL_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

// PICS header constants (must match parallelstrips.go).
#define PICS_MAGIC        "PICS"
#define PICS_HEADER_BASE  20   // 4+4+4+4+4 bytes before the offset table

// Decompress a PICS blob produced by CompressParallelStrips.
//
// compressed / compressed_len : the full PICS byte blob
// pixels_out                  : caller-allocated, width * height uint16 values
// width / height              : must match the values stored in the PICS header
// max_threads                 : maximum concurrent pthread workers;
//                               0 = use all strips as independent threads
//
// Returns 0 on success, non-zero on error.
//
// Thread safety: multiple calls may run concurrently as long as they write to
// different pixels_out buffers.
int mic_decompress_parallel(const uint8_t *compressed, size_t compressed_len,
                            uint16_t *pixels_out, int width, int height,
                            int max_threads);

// Same interface but forces the scalar four-state inner decoder even on AMD64.
// Useful for benchmarking to isolate thread-level vs. instruction-level gains.
int mic_decompress_parallel_scalar(const uint8_t *compressed, size_t compressed_len,
                                   uint16_t *pixels_out, int width, int height,
                                   int max_threads);

#ifdef __cplusplus
}
#endif

#endif // MIC_PARALLEL_H
