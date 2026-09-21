// jpegls-adapter.mjs — JPEG-LS live browser decode via the vendored CharLS
// decode-only WASM build (design §4.4). The probe (scripts/probe-codecs.mjs)
// confirmed this decodes the reference .jls files to bit-exact 16-bit grayscale.

import { loadEmscriptenModule, decodeWithEmbindDecoder } from './emscripten-loader.mjs';

const JS_URL = new URL('../vendor/charls/charlswasm_decode.js', import.meta.url);
const WASM_URL = new URL('../vendor/charls/charlswasm_decode.wasm', import.meta.url);

export function makeJPEGLSAdapter({ id = 'jpegls', label = 'JPEG-LS' } = {}) {
  let Module = null;
  return {
    id,
    label,
    liveDecodeSupported: true,
    async init() {
      if (!Module) Module = await loadEmscriptenModule(JS_URL.href, WASM_URL.href);
    },
    async decode(bytes) {
      return decodeWithEmbindDecoder(Module, 'JpegLSDecoder', bytes);
    },
  };
}
