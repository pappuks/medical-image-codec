// jxl-adapter.mjs — JPEG-XL informational adapter.
//
// JPEG-XL is NOT decoded live in the browser. The only mature browser JXL
// decoder (@jsquash/jxl) exposes a decode() that returns an 8-bit RGBA
// ImageData, which cannot represent lossless 16-bit grayscale medical pixels
// (confirmed by scripts/probe-codecs.mjs). So this adapter is informational:
// the dashboard shows JXL's real compressed size (from refcodecs-manifest.json,
// produced by `mic-refgen`) and a native-C reference decode throughput from
// pacs-model.mjs (REFERENCE_NATIVE.jxl), clearly flagged as not-a-live-number.
//
// If a future WASM build surfaces 16-bit grayscale output, flip
// liveDecodeSupported to true and implement decode() — nothing else in the
// dashboard's dispatch needs to change (design §6.4).

export function makeJXLAdapter({ id = 'jxl', label = 'JPEG-XL' } = {}) {
  return {
    id,
    label,
    liveDecodeSupported: false,
    async init() {},
    // no decode(): informational-only. The dashboard uses REFERENCE_NATIVE.
  };
}
