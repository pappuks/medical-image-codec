// pacs-runner.mjs — Orchestration for the browser PACS benchmark: fetch every
// codec's compressed file, time live decodes (warmup + median, matching the
// Node script's methodology), optionally verify pixel-correctness against the
// manifest, and emit a flat result structure the dashboard renders and the
// headless runner asserts on. No DOM here — this is drivable from a test.

import {
  IMAGES, CODEC_REGISTRY, REFERENCE_NATIVE, fnv1a32Hex,
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

// Informational (non-live) decode time from the native-C reference table.
function informationalDecodeMs(manifestKey, imgName, rawBytes) {
  const ref = REFERENCE_NATIVE[manifestKey];
  const mbps = ref?.decompMBps?.[imgName];
  if (mbps == null) return null;
  return (rawBytes / (1024 * 1024)) / mbps * 1000;
}

// runBenchmark — main entry.
//   opts.images       : subset of IMAGES to run (default all)
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

  const records = [];
  const total = images.length * codecs.length;
  let done = 0;

  for (const img of images) {
    const rawBytes = img.w * img.h * 2;
    for (const codec of codecs) {
      const adapter = adapters.get(codec.id);
      const label = codec.label;
      onProgress({ done, total, label: `${img.name} / ${label}` });

      const rec = {
        image: img.name, modality: img.modality, width: img.w, height: img.h,
        rawBytes, codecId: codec.id, label, kind: codec.kind,
        liveDecode: adapter.liveDecodeSupported, compressedBytes: null,
        decodeMs: null, ratio: null, pixelsVerified: null, note: null,
      };

      try {
        if (adapter.liveDecodeSupported) {
          // Fetch the compressed file (first existing candidate).
          let bytes = null;
          for (const p of candidatePaths(codec, img.name)) {
            bytes = await fetchBytes(baseUrl, p, fetchFn);
            if (bytes) break;
          }
          if (!bytes) { rec.note = 'file missing'; done++; records.push(rec); continue; }
          rec.compressedBytes = bytes.length;
          rec.ratio = rawBytes / bytes.length;
          rec.decodeMs = await timeMedian(adapter, bytes, iterations, warmup);

          if (verify && rawManifest) {
            const want = rawManifest.images?.[img.name]?.checksum;
            const { pixels } = await adapter.decode(bytes);
            const got = checksumOfPixels(pixels);
            rec.pixelsVerified = want != null ? (got === want) : null;
            if (want != null && got !== want) rec.note = `checksum ${got} != ${want}`;
          }
        } else {
          // Informational codec: real size from refcodecs-manifest.json when
          // present, native-C reference decode throughput either way.
          const refEntry = refManifest?.images?.[img.name]?.[codec.manifestKey];
          if (refEntry?.bytes != null) {
            rec.compressedBytes = refEntry.bytes;
          } else {
            const ratio = REFERENCE_NATIVE[codec.manifestKey]?.ratio?.[img.name];
            if (ratio != null) rec.compressedBytes = Math.round(rawBytes / ratio);
          }
          if (rec.compressedBytes != null) rec.ratio = rawBytes / rec.compressedBytes;
          rec.decodeMs = informationalDecodeMs(codec.manifestKey, img.name, rawBytes);
          rec.note = 'native-C reference (no browser decoder)';
          if (rec.compressedBytes == null || rec.decodeMs == null) rec.note = 'no reference data';
        }
      } catch (e) {
        rec.note = `error: ${e.message}`;
      }

      done++;
      records.push(rec);
    }
  }

  onProgress({ done: total, total, label: 'complete' });

  for (const a of adapters.values()) if (typeof a.dispose === 'function') a.dispose();

  const env = collectEnv();
  // Surface the PICS SAB mode if a PICS adapter ran.
  for (const a of adapters.values()) if (a.sabMode != null) env.picsSabMode = a.sabMode;

  return {
    generatedAt: new Date().toISOString(),
    iterations, warmup, verify,
    manifestPresent: !!rawManifest,
    refManifestPresent: !!refManifest,
    env,
    records,
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
