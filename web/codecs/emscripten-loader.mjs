// emscripten-loader.mjs — Load a UMD Emscripten module (OpenJPH / CharLS) from
// the vendored static tree into an ESM context.
//
// These vendored decoders are classic UMD Emscripten bundles: they end with
//   if (typeof exports === 'object' && typeof module === 'object')
//     module.exports = <Factory>;
// and expose nothing under ESM. So an ESM `import` can't reach the factory. We
// fetch the glue source and run it inside a `new Function(module, exports, ...)`
// wrapper that supplies a CommonJS-like `module`/`exports`, capturing the
// factory. We then call the factory with a `locateFile` override so Emscripten
// fetches the sibling .wasm from our vendored path (design §4.4/§4.5).
//
// Requires no CSP 'unsafe-eval' exception to be *removed* — the dashboard adds
// no CSP at all (design §6.6), so `new Function` is permitted. There is no
// remote code: the source is our own committed, vendored file.

// Instantiate an Emscripten factory from already-fetched glue source.
// `locateWasm(filename)` returns the URL/path Emscripten should load the .wasm
// from. Returns the initialized Module (embind classes attached).
export async function instantiateFromSource(src, locateWasm) {
  const make = new Function('module', 'exports', src + '\n;return module.exports;');
  const mod = { exports: {} };
  const factory = make(mod, mod.exports);
  if (typeof factory !== 'function') {
    throw new Error('emscripten-loader: module did not export a factory function');
  }
  return await factory({
    locateFile: (path) => (path.endsWith('.wasm') ? locateWasm(path) : path),
  });
}

// Browser entry: fetch the glue JS and instantiate, loading the .wasm from
// wasmUrl. Both URLs are typically built with new URL('./vendor/...', import.meta.url).
export async function loadEmscriptenModule(jsUrl, wasmUrl) {
  const resp = await fetch(jsUrl);
  if (!resp.ok) throw new Error(`emscripten-loader: fetch ${jsUrl} -> ${resp.status}`);
  const src = await resp.text();
  return instantiateFromSource(src, () => wasmUrl);
}

// Shared decode helper for the Cornerstone-style embind decoders (OpenJPH,
// CharLS): both expose an identical decoder-instance shape.
//   const d = new Module[DecoderClass]();
//   d.getEncodedBuffer(len) -> heap view; .set(encoded)
//   d.decode()
//   d.getFrameInfo() -> { width, height, bitsPerSample, componentCount, isSigned? }
//   d.getDecodedBuffer() -> heap byte view of decoded samples
//   d.delete()
// Returns { pixels: Uint16Array, width, height, bitsPerSample, signed }.
export function decodeWithEmbindDecoder(Module, DecoderClass, encoded) {
  const Decoder = Module[DecoderClass];
  if (!Decoder) throw new Error(`emscripten-loader: ${DecoderClass} not found on Module`);
  const decoder = new Decoder();
  try {
    const inBuf = decoder.getEncodedBuffer(encoded.length);
    inBuf.set(encoded);
    decoder.decode();
    const fi = decoder.getFrameInfo();
    const decoded = decoder.getDecodedBuffer(); // Uint8Array view into wasm heap
    const bitsPerSample = fi.bitsPerSample;
    let pixels;
    if (bitsPerSample > 8) {
      // Copy out of the wasm heap (the view is invalidated once decoder.delete()
      // runs / heap grows) as little-endian uint16 samples.
      const n = decoded.byteLength >>> 1;
      pixels = new Uint16Array(n);
      for (let i = 0; i < n; i++) {
        pixels[i] = decoded[i * 2] | (decoded[i * 2 + 1] << 8);
      }
    } else {
      pixels = Uint16Array.from(decoded);
    }
    return {
      pixels,
      width: fi.width,
      height: fi.height,
      bitsPerSample,
      signed: !!fi.isSigned,
    };
  } finally {
    if (typeof decoder.delete === 'function') decoder.delete();
  }
}
