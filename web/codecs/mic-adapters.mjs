// mic-adapters.mjs — Adapters wrapping the existing, already-correct MIC
// decoders into the common codec-adapter interface (codec-interface.md).
// No new decode logic here — this only adapts the existing API shape.

import { MICDecoder } from '../mic-decoder.js';
import { createPICSDecoder } from '../mic-decoder-parallel.js';

// Single-threaded MIC FSE variant (1/4/8-state — the state count is auto-
// detected by MICDecoder from the stream's magic prefix, so one adapter covers
// all three; the difference is purely which .mic file the dashboard feeds in).
export function makeMICAdapter({ id, label }) {
  return {
    id,
    label,
    liveDecodeSupported: true,
    async init() {},
    async decode(bytes) {
      const { pixels, width, height } = MICDecoder.decodeFile(bytes);
      return { pixels, width, height, bitsPerSample: 16, signed: false };
    },
  };
}

// PICS parallel-strip variant, decoded across a real Web Worker pool (the
// browser analogue of the Node script's worker_threads pool). One pool is
// created in init() and reused across all timed decodes.
export function makePICSAdapter({ id, label }) {
  let decoder = null;
  return {
    id,
    label,
    liveDecodeSupported: true,
    sabMode: null, // populated after first decode; surfaced in the env panel
    async init() {
      decoder = await createPICSDecoder(); // sizes pool from hardwareConcurrency
    },
    async decode(bytes) {
      const r = await decoder.decodePICS(bytes);
      this.sabMode = r.sabMode;
      return { pixels: r.pixels, width: r.width, height: r.height, bitsPerSample: 16, signed: false };
    },
    dispose() {
      if (decoder) decoder.terminate();
      decoder = null;
    },
  };
}
