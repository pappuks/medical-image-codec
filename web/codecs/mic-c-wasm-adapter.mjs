// mic-c-wasm-adapter.mjs — MIC decoded by the pure-C implementation
// (ojph/mic_decompress_c.c) compiled to WebAssembly with Emscripten
// (web/wasm-c/build.sh -> web/vendor/mic-c/). This is the third way the
// dashboard decodes the same .mic bytes: pure JS, Go/WASM, and now C/WASM —
// the C build is ~20 KB of wasm (no language runtime) vs the Go build's ~2.9 MB.
//
// The C decoder takes the raw FSE payload (the bytes at offset 20 of a MIC1
// container, starting with the 0xFF,0xNN state marker), so this adapter parses
// the MIC1 header itself and passes the payload + dimensions + state count.

import { loadEmscriptenModule } from './emscripten-loader.mjs';

const JS_URL = new URL('../vendor/mic-c/mic-c-decoder.js', import.meta.url);
const WASM_URL = new URL('../vendor/mic-c/mic-c-decoder.wasm', import.meta.url);

const MIC1_MAGIC = 0x3143494d; // "MIC1" little-endian

// state: 2 | 4 | 8 — which N-state decoder to dispatch to (must match the file).
export function makeMICCWasmAdapter({ id, label, state = 4 } = {}) {
  let Module = null;
  return {
    id,
    label,
    liveDecodeSupported: true,
    async init() {
      if (!Module) Module = await loadEmscriptenModule(JS_URL.href, WASM_URL.href);
    },
    async decode(bytes) {
      const dv = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
      if (dv.getUint32(0, true) !== MIC1_MAGIC) {
        throw new Error('MIC-C-WASM: not a MIC1 single-frame container');
      }
      const width = dv.getUint32(4, true);
      const height = dv.getUint32(8, true);
      const compLen = dv.getUint32(16, true);
      const payload = bytes.subarray(20, 20 + compLen);

      const inPtr = Module._malloc(payload.length);
      const outPtr = Module._malloc(width * height * 2);
      try {
        // Grab HEAPU8 fresh (a _malloc may have grown & replaced the views).
        Module.HEAPU8.set(payload, inPtr);
        const rc = Module._mic_c_decode(inPtr, payload.length, outPtr, width, height, state);
        if (rc !== 0) throw new Error(`mic_c_decode (state=${state}) rc=${rc}`);
        // Decode may have grown memory — read HEAPU16 fresh, then copy out of
        // the heap (slice) before freeing so the result outlives the buffers.
        const pixels = Module.HEAPU16.slice(outPtr >> 1, (outPtr >> 1) + width * height);
        return { pixels, width, height, bitsPerSample: 16, signed: false };
      } finally {
        Module._free(inPtr);
        Module._free(outPtr);
      }
    },
  };
}
