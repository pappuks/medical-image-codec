// mic_c_wasm.c — Emscripten entry wrapper around the pure-C MIC decoder
// (ojph/mic_decompress_c.c), so the same C implementation that powers the
// MIC-*-C benchmark variants can be compiled to WebAssembly and compared, in
// the PACS dashboard, against pure-JS MIC and the Go/WASM MIC build on
// identical .mic bytes.
//
// The C decoder's x86 SIMD paths are gated by `#if defined(__x86_64__)`, so a
// wasm32 build automatically uses the portable scalar fallbacks — no SIMD
// intrinsics reach the WASM compiler. (A future WASM-SIMD build would target
// the _simd entry points with `-msimd128`; not done here.)
//
// Build: web/wasm-c/build.sh  (see that script for the emcc invocation).

#include "mic_decompress_c.h"
#include <emscripten.h>

// mic_c_decode dispatches to the scalar N-state decoder. `state` is 2, 4 or 8;
// `comp` is the raw FSE payload (starting with the 0xFF,0xNN state marker — the
// bytes at offset 20 of a MIC1 container), NOT the .mic container itself. The
// caller (JS adapter) parses the container and passes the payload + dimensions.
// Returns 0 on success, non-zero on error.
EMSCRIPTEN_KEEPALIVE
int mic_c_decode(const uint8_t *comp, int comp_len,
                 uint16_t *out, int width, int height, int state) {
    switch (state) {
        case 8:  return mic_decompress_eight_state(comp, (size_t)comp_len, out, width, height);
        case 4:  return mic_decompress_four_state(comp, (size_t)comp_len, out, width, height);
        default: return mic_decompress_two_state(comp, (size_t)comp_len, out, width, height);
    }
}
