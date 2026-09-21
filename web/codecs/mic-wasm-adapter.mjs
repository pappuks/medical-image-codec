// mic-wasm-adapter.mjs — MIC decoded by the Go codec compiled to WebAssembly
// (cmd/mic-wasm -> mic-decoder.wasm), as a live browser codec alongside the
// pure-JS MICDecoder. This lets the dashboard compare the same .mic bytes
// decoded three ways: pure JS, Go/WASM, and (for reference) the C-based
// HTJ2K/JPEG-LS WASM decoders.
//
// The Go WASM decodeFile auto-detects the FSE state count (DecompressSingleFrame
// -> FSEDecompressU16Auto), so it decodes any .mic; the registry pairs it with
// the 4-state stream for a direct pure-JS-4state vs Go-WASM-4state comparison.
//
// Requires the WASM to be built:
//   GOOS=js GOARCH=wasm go build -o web/mic-decoder.wasm ./cmd/mic-wasm/
//   cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" web/wasm_exec.js
// If mic-decoder.wasm is absent, init() throws and the runner records the row
// as unavailable (it does not abort the whole run — the pool is disposed).

import { loadMICWasm } from '../mic-decoder-wasm.js';

export function makeMICWasmAdapter({ id = 'mic-wasm', label = 'MIC-WASM (Go)' } = {}) {
  let decoder = null;
  return {
    id,
    label,
    liveDecodeSupported: true,
    async init() {
      if (!decoder) {
        // Paths are resolved relative to the document (web/), where both the
        // .wasm and wasm_exec.js live.
        decoder = await loadMICWasm(new URL('../mic-decoder.wasm', import.meta.url).href);
      }
    },
    async decode(bytes) {
      const { pixels, width, height } = decoder.decodeFile(bytes);
      return { pixels, width, height, bitsPerSample: 16, signed: false };
    },
  };
}
