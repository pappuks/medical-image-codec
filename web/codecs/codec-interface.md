# Codec adapter interface

Every codec the PACS dashboard benchmarks — MIC (1/4/8-state), PICS (4/8
strips), HTJ2K, JPEG-LS, JPEG-XL — is wrapped in an adapter implementing this
duck-typed interface, so the dashboard's timing/correctness loops are written
once against a single shape (design §4.4).

```js
// Adapter shape (no TypeScript in this repo — documented contract only):
{
  id: string,               // e.g. 'mic-4state', 'pics-8', 'htj2k', 'jpegls', 'jxl'
  label: string,            // display label
  liveDecodeSupported: bool,// false => this row is informational (native-C reference
                            //          numbers), NOT a live browser measurement

  async init(),             // one-time WASM instantiation / worker-pool spin-up.
                            //   Called once, OUTSIDE all timing loops. No-op for
                            //   informational adapters.

  async decode(bytes),      // decode one compressed image.
                            //   -> { pixels: Uint16Array, width, height,
                            //        bitsPerSample?, signed? }
                            //   Only defined when liveDecodeSupported === true.

  dispose?(),               // terminate workers / free wasm. Optional.
}
```

## Which codecs are live vs. informational

| Codec | Adapter | Live? | Why |
|-------|---------|-------|-----|
| MIC-1/4/8-state | `mic-adapters.mjs` | ✅ | pure-JS `MICDecoder.decodeFile` |
| PICS-4/8 | `mic-adapters.mjs` | ✅ | `createPICSDecoder()` real Web Worker pool |
| HTJ2K | `htj2k-adapter.mjs` | ✅ | vendored OpenJPH WASM; probe confirmed 16-bit lossless |
| JPEG-LS | `jpegls-adapter.mjs` | ✅ | vendored CharLS WASM; probe confirmed 16-bit lossless |
| JPEG-XL | `jxl-adapter.mjs` | ❌ | `@jsquash/jxl` decode() returns 8-bit RGBA `ImageData`; cannot preserve lossless 16-bit grayscale (see `scripts/probe-codecs.mjs`) |

Informational adapters (`liveDecodeSupported: false`) have no `decode()`; the
dashboard fills their decode time from the native-C reference table in
`pacs-model.mjs` (`REFERENCE_NATIVE`) and their size from the real `.jxl` file
in `refcodecs-manifest.json` when present.
