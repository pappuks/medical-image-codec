// pacs-runner.mjs — Orchestration for the browser PACS benchmark: fetch every
// codec's compressed file, time live decodes (warmup + median, matching the
// Node script's methodology), optionally verify pixel-correctness against the
// manifest, and emit a flat result structure the dashboard renders and the
// headless runner asserts on. No DOM here — this is drivable from a test.

import {
  IMAGES, CODEC_REGISTRY, REFERENCE_NATIVE, CINE_DATASETS, cineFrameImages, fnv1a32Hex,
} from './pacs-model.mjs';
import { makeAdapter } from './codecs/index.mjs';

// Default timing knobs (overridable via the dashboard / URL params). Match
// bench-pacs-viewer.mjs so browser and Node numbers are comparable.
export const DEFAULT_ITERATIONS = 15;
export const DEFAULT_WARMUP = 3;

const nextFrame = () =>
  new Promise((r) => (typeof requestAnimationFrame === 'function'
    ? requestAnimationFrame(() => r())
    : setTimeout(r, 0)));

// Resolve the testdata file URL(s) for a codec+image; returns an ordered list
// of candidate paths (first existing wins). Relative to baseUrl.
function candidatePaths(codec, imgName) {
  // Each image ships exactly one PICS variant (_pics4 for small images, _pics8
  // for large ones). Load only the codec's own strip file — never fall back
  // across strip counts, which would show one strip count under another's label.
  if (codec.kind === 'mic' || codec.kind === 'micwasm' || codec.kind === 'miccwasm'
      || codec.kind === 'pics' || codec.kind === 'picscwasm') {
    return [`testdata/${imgName}${codec.suffix}.mic`];
  }
  if (codec.kind === 'wasm') return [`testdata/${imgName}.${codec.ext}`];
  return [];
}

async function fetchBytes(baseUrl, path, fetchFn) {
  const url = new URL(path, baseUrl).href;
  const resp = await fetchFn(url);
  if (!resp.ok) return null;
  const buf = await resp.arrayBuffer();
  return new Uint8Array(buf);
}

async function fetchJSON(baseUrl, path, fetchFn) {
  try {
    const resp = await fetchFn(new URL(path, baseUrl).href);
    if (!resp.ok) return null;
    return await resp.json();
  } catch { return null; }
}

function checksumOfPixels(pixels) {
  return fnv1a32Hex(new Uint8Array(pixels.buffer, pixels.byteOffset, pixels.byteLength));
}

// Warmup + median timing of a live adapter decode. Yields to the event loop
// BETWEEN timed decodes only (never within one) so the UI stays responsive
// without distorting the measurement (design §6.1).
async function timeMedian(adapter, bytes, iterations, warmup) {
  for (let i = 0; i < warmup; i++) await adapter.decode(bytes);
  const times = [];
  for (let i = 0; i < iterations; i++) {
    const t0 = performance.now();
    await adapter.decode(bytes);
    times.push(performance.now() - t0);
    await nextFrame();
  }
  times.sort((a, b) => a - b);
  return times[Math.floor(times.length / 2)];
}

// Informational (non-live) decode time from the native-C reference table. For
// cine frames (image names like CINE_XA_f003, absent from the per-image table)
// it falls back to the codec's corpus-average decode throughput so JXL still
// gets a plausible informational number in the cine section.
function informationalDecodeMs(manifestKey, imgName, rawBytes) {
  const ref = REFERENCE_NATIVE[manifestKey];
  if (!ref) return null;
  let mbps = ref.decompMBps?.[imgName];
  if (mbps == null) {
    const vals = Object.values(ref.decompMBps || {});
    if (!vals.length) return null;
    mbps = vals.reduce((a, b) => a + b, 0) / vals.length;
  }
  return (rawBytes / (1024 * 1024)) / mbps * 1000;
}

// Measure one codec on one image/frame: fetch the compressed file, time the
// live decode (warmup + median) and optionally verify pixels, OR fall back to
// the informational native-C reference. Returns a flat core measurement reused
// by both the per-image loop and the per-frame cine loop.
async function measureOne(adapter, codec, img, ctx) {
  const rawBytes = img.w * img.h * 2;
  const out = {
    liveDecode: adapter.liveDecodeSupported, compressedBytes: null,
    decodeMs: null, ratio: null, pixelsVerified: null, note: null,
  };
  if (adapter.liveDecodeSupported) {
    let bytes = null;
    for (const p of candidatePaths(codec, img.name)) {
      bytes = await fetchBytes(ctx.baseUrl, p, ctx.fetchFn);
      if (bytes) break;
    }
    if (!bytes) { out.note = 'file missing'; return out; }
    out.compressedBytes = bytes.length;
    out.ratio = rawBytes / bytes.length;
    out.decodeMs = await timeMedian(adapter, bytes, ctx.iterations, ctx.warmup);
    if (ctx.verify && ctx.rawManifest) {
      const want = ctx.rawManifest.images?.[img.name]?.checksum;
      const { pixels } = await adapter.decode(bytes);
      const got = checksumOfPixels(pixels);
      out.pixelsVerified = want != null ? (got === want) : null;
      if (want != null && got !== want) out.note = `checksum ${got} != ${want}`;
    }
  } else {
    const refEntry = ctx.refManifest?.images?.[img.name]?.[codec.manifestKey];
    if (refEntry?.bytes != null) {
      out.compressedBytes = refEntry.bytes;
    } else {
      const ratio = REFERENCE_NATIVE[codec.manifestKey]?.ratio?.[img.name];
      if (ratio != null) out.compressedBytes = Math.round(rawBytes / ratio);
    }
    if (out.compressedBytes != null) out.ratio = rawBytes / out.compressedBytes;
    out.decodeMs = informationalDecodeMs(codec.manifestKey, img.name, rawBytes);
    out.note = 'native-C reference (no browser decoder)';
    if (out.compressedBytes == null || out.decodeMs == null) out.note = 'no reference data';
  }
  return out;
}

// runBenchmark — main entry.
//   opts.images       : subset of IMAGES to run (default all)
//   opts.cine         : subset of CINE_DATASETS to run (default all; [] to skip)
//   opts.codecs       : subset of CODEC_REGISTRY (default all)
//   opts.iterations   : timed iterations per codec/image
//   opts.warmup       : warmup decodes
//   opts.verify       : also run the pixel-correctness pass
//   opts.baseUrl      : base URL for testdata fetches (default location.href)
//   opts.fetchFn      : fetch implementation (default global fetch)
//   opts.onProgress   : ({done,total,label}) => void
// Returns { generatedAt, env, records, manifestPresent, refManifestPresent }.
export async function runBenchmark(opts = {}) {
  const images = opts.images ?? IMAGES;
  const cineDatasets = opts.cine ?? CINE_DATASETS;
  const codecs = opts.codecs ?? CODEC_REGISTRY;
  const iterations = opts.iterations ?? DEFAULT_ITERATIONS;
  const warmup = opts.warmup ?? DEFAULT_WARMUP;
  const verify = opts.verify ?? false;
  const baseUrl = opts.baseUrl ?? (typeof location !== 'undefined' ? location.href : 'http://localhost/');
  const fetchFn = opts.fetchFn ?? fetch;
  const onProgress = opts.onProgress ?? (() => {});

  const rawManifest = await fetchJSON(baseUrl, 'testdata/manifest.json', fetchFn);
  const refManifest = await fetchJSON(baseUrl, 'testdata/refcodecs-manifest.json', fetchFn);

  // Build + init adapters once, outside all timing loops. If any init() throws
  // (e.g. a WASM decoder wasn't vendored), dispose the adapters already created
  // — notably the PICS Web Worker pool — before rethrowing, so a failed run
  // never leaks live workers.
  const adapters = new Map();
  try {
    for (const codec of codecs) {
      const a = makeAdapter(codec);
      await a.init();
      adapters.set(codec.id, a);
    }
  } catch (e) {
    for (const a of adapters.values()) if (typeof a.dispose === 'function') a.dispose();
    throw e;
  }

  const ctx = { baseUrl, fetchFn, iterations, warmup, verify, rawManifest, refManifest };

  const records = [];
  const cineRecords = [];
  const total = (images.length + cineDatasets.length) * codecs.length;
  let done = 0;

  for (const img of images) {
    const rawBytes = img.w * img.h * 2;
    for (const codec of codecs) {
      const adapter = adapters.get(codec.id);
      onProgress({ done, total, label: `${img.name} / ${codec.label}` });

      const rec = {
        image: img.name, modality: img.modality, width: img.w, height: img.h,
        rawBytes, codecId: codec.id, label: codec.label, kind: codec.kind,
        liveDecode: adapter.liveDecodeSupported, compressedBytes: null,
        decodeMs: null, ratio: null, pixelsVerified: null, note: null,
      };
      try {
        Object.assign(rec, await measureOne(adapter, codec, img, ctx));
      } catch (e) {
        rec.note = `error: ${e.message}`;
      }
      done++;
      records.push(rec);
    }
  }

  // Cine / multi-frame section: decode every frame of each dataset independently
  // and aggregate to a full-loop time and frames/s, per (dataset, codec).
  for (const ds of cineDatasets) {
    const frames = cineFrameImages(ds);
    const rawPerFrame = ds.w * ds.h * 2;
    for (const codec of codecs) {
      const adapter = adapters.get(codec.id);
      onProgress({ done, total, label: `${ds.id} (cine) / ${codec.label}` });

      const rec = {
        cine: ds.id, datasetLabel: ds.label, modality: ds.modality,
        width: ds.w, height: ds.h, bits: ds.bits, frames: ds.frames,
        codecId: codec.id, label: codec.label, kind: codec.kind,
        liveDecode: adapter.liveDecodeSupported,
        framesMeasured: 0, rawBytesTotal: rawPerFrame * ds.frames,
        compressedBytesTotal: null, ratio: null,
        loopMs: null, msPerFrame: null, fps: null,
        pixelsVerified: null, note: null,
      };
      try {
        let comp = 0, dec = 0, verifiedSeen = false, allVerified = true;
        for (const fr of frames) {
          const m = await measureOne(adapter, codec, fr, ctx);
          if (m.compressedBytes == null && m.decodeMs == null) {
            if (!rec.note) rec.note = m.note; // e.g. 'file missing'
            continue;
          }
          rec.framesMeasured++;
          if (m.compressedBytes != null) comp += m.compressedBytes;
          if (m.decodeMs != null) dec += m.decodeMs;
          if (m.pixelsVerified === true) verifiedSeen = true;
          else if (m.pixelsVerified === false) { verifiedSeen = true; allVerified = false; if (!rec.note) rec.note = m.note; }
        }
        if (rec.framesMeasured > 0) {
          rec.compressedBytesTotal = comp || null;
          rec.ratio = comp ? rec.rawBytesTotal / comp : null;
          rec.loopMs = dec || null;
          rec.msPerFrame = dec ? dec / rec.framesMeasured : null;
          rec.fps = dec ? 1000 / (dec / rec.framesMeasured) : null;
          rec.pixelsVerified = verifiedSeen ? allVerified : null;
          if (!rec.liveDecode && !rec.note) rec.note = 'native-C reference (no browser decoder)';
        } else if (!rec.note) {
          rec.note = 'no frames';
        }
      } catch (e) {
        rec.note = `error: ${e.message}`;
      }
      done++;
      cineRecords.push(rec);
    }
  }

  onProgress({ done: total, total, label: 'complete' });

  const env = collectEnv();
  // Surface the PICS SAB mode if a PICS adapter ran (before disposing them).
  for (const a of adapters.values()) if (a.sabMode != null) env.picsSabMode = a.sabMode;

  for (const a of adapters.values()) if (typeof a.dispose === 'function') a.dispose();

  return {
    generatedAt: new Date().toISOString(),
    iterations, warmup, verify,
    manifestPresent: !!rawManifest,
    refManifestPresent: !!refManifest,
    env,
    records,
    cineRecords,
  };
}

function collectEnv() {
  const env = { runtime: 'browser' };
  if (typeof navigator !== 'undefined') {
    env.userAgent = navigator.userAgent;
    env.hardwareConcurrency = navigator.hardwareConcurrency;
  }
  if (typeof crossOriginIsolated !== 'undefined') env.crossOriginIsolated = crossOriginIsolated;
  return env;
}
