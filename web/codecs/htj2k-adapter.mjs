// htj2k-adapter.mjs — HTJ2K (High-Throughput JPEG 2000) live browser decode via
// the vendored OpenJPH WASM build (design §4.4). The probe
// (scripts/probe-codecs.mjs) confirmed this decodes the reference .jph files to
// bit-exact 16-bit grayscale.

import { loadEmscriptenModule, decodeWithEmbindDecoder } from './emscripten-loader.mjs';

const JS_URL = new URL('../vendor/openjph/openjphjs.js', import.meta.url);
const WASM_URL = new URL('../vendor/openjph/openjphjs.wasm', import.meta.url);

export function makeHTJ2KAdapter({ id = 'htj2k', label = 'HTJ2K' } = {}) {
  let Module = null;
  return {
    id,
    label,
    liveDecodeSupported: true,
    async init() {
      if (!Module) Module = await loadEmscriptenModule(JS_URL.href, WASM_URL.href);
    },
    async decode(bytes) {
      return decodeWithEmbindDecoder(Module, 'HTJ2KDecoder', bytes);
    },
  };
}
