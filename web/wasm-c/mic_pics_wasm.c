// mic_pics_wasm.c — Emscripten entry for the pure-C PICS parallel-strip decoder
// (ojph/mic_parallel.c) compiled to WASM with pthreads. Each strip is decoded
// on its own pthread (a Web Worker under Emscripten), mirroring the Go
// goroutine pool and the JS Web Worker pool — a third parallel-decode path for
// the dashboard, this one with the actual C scheduler + C inner decoder.
//
// Because pthread_join blocks, mic_pics_decode MUST be called off the browser
// main thread (the adapter runs it inside a dedicated Web Worker). The x86 SIMD
// inner path is __x86_64__-gated, so wasm uses the scalar four/two-state
// decoders — hence we call the _scalar variant explicitly.
//
// Build: web/wasm-c/build-pics.sh

#include "mic_parallel.h"
#include <emscripten.h>

// comp: full PICS blob ("PICS"…); out: width*height uint16; max_threads: pool cap.
// Returns 0 on success, non-zero on error.
EMSCRIPTEN_KEEPALIVE
int mic_pics_decode(const uint8_t *comp, int comp_len,
                    uint16_t *out, int width, int height, int max_threads) {
    return mic_decompress_parallel_scalar(comp, (size_t)comp_len, out,
                                          width, height, max_threads);
}
