// pacs-model.mjs — Shared source of truth for the PACS web-viewer benchmark.
//
// This module is pure ESM with NO Node-only (fs, worker_threads) and NO
// browser-only (Worker, document, fetch) globals, so it loads unmodified in
// BOTH the Node script (bench-pacs-viewer.mjs) and the browser dashboard
// (pacs-dashboard.mjs). Everything here is data + pure math: network profiles,
// the test-image set, the codec registry, transfer/study timing, and the
// formatting helpers used by both console and DOM reports.
//
// Rule: if a value or function is needed identically by both runners, it lives
// here. Anything that needs a Worker, WASM instantiation, fs, or the DOM stays
// in the runner-specific files.

// ---------------------------------------------------------------------------
// Network profiles — representative PACS deployment scenarios, best to worst.
// transferMs()/simulateStudy() model bandwidth-bound transfer plus one paid
// RTT (persistent HTTP/2 connection to the PACS server).
// ---------------------------------------------------------------------------
export const NETWORK_PROFILES = [
  { name: 'Gigabit LAN',        mbps: 1000, rttMs: 1,   note: 'modern radiology workstation, wired 1G switch' },
  { name: 'Hospital LAN',       mbps: 100,  rttMs: 2,   note: 'radiology workstation, wired' },
  { name: 'Hospital Wi-Fi',     mbps: 20,   rttMs: 10,  note: 'mobile cart / tablet on-site' },
  { name: 'Home Broadband',     mbps: 25,   rttMs: 20,  note: 'remote read, VPN' },
  { name: '5G',                 mbps: 100,  rttMs: 20,  note: 'remote read, 5G mobile / hotspot' },
  { name: 'Cellular (4G/LTE)',  mbps: 5,    rttMs: 60,  note: 'remote read, on-call physician' },
  { name: '3G',                 mbps: 1.5,  rttMs: 100, note: 'legacy cellular fallback, rural / backup link' },
  { name: 'Congested / Sat.',   mbps: 1,    rttMs: 600, note: 'satellite backhaul or severely congested VPN — worst case' },
];

// A representative 4-profile subset for compact/default table views (the
// dashboard shows these by default, with a "show all 8" toggle).
export const DEFAULT_PROFILE_NAMES = ['Gigabit LAN', 'Hospital Wi-Fi', 'Cellular (4G/LTE)', 'Congested / Sat.'];

export function transferMs(bytes, mbps) {
  return (bytes * 8) / (mbps * 1e6) * 1000;
}

// ---------------------------------------------------------------------------
// Test images (single-frame grayscale, all present in web/testdata/).
// Names match the .mic files emitted by `mic-compress -testdata` and the
// reference files emitted by `mic-refgen`.
// ---------------------------------------------------------------------------
export const IMAGES = [
  { name: 'MR',       modality: 'MRI',                  w: 256,  h: 256 },
  { name: 'CT',       modality: 'CT',                   w: 512,  h: 512 },
  { name: 'PET1',     modality: 'PET',                  w: 256,  h: 256 },
  { name: 'DX_HAND',  modality: 'Digital X-ray',        w: 1410, h: 1480 },
  { name: 'CR',       modality: 'Computed Radiography', w: 1760, h: 2140 },
  { name: 'MG1',      modality: 'Mammography',          w: 1996, h: 2457 },
  { name: 'MG2',      modality: 'Mammography',          w: 1996, h: 2457 },
  { name: 'MG3',      modality: 'Mammography (large)',  w: 3064, h: 4774 },
];

// A small subset for quick smoke runs (dashboard "Quick" mode, headless CI).
export const QUICK_IMAGE_NAMES = ['MR', 'CT', 'CR'];

// ---------------------------------------------------------------------------
// Cine / multi-frame datasets. Each is a real, public-domain multi-frame DICOM
// whose EVERY frame is emitted as an INDEPENDENT single-frame image
// (<id>_f<NNN>) by `mic-compress -testdata` and `mic-refgen`. Treating each
// frame as its own image lets the full single-frame codec matrix (MIC 1/4/8
// -state, PICS, HTJ2K, JPEG-LS, JPEG-XL) run per frame, so the benchmark reports
// cine decode throughput (full-loop ms + frames/s) by fetching and decoding each
// frame independently — exactly how a PACS viewer streams a cine loop.
//
// frames/w/h/bits/pics MUST match cmd/mic-compress/main.go `cineDatasets` output
// (frame counts come straight from the source DICOMs). Sources are fetched by
// testdata/multiframe/fetch-cine-sources.sh.
// ---------------------------------------------------------------------------
export const CINE_DATASETS = [
  { id: 'CINE_MRCARD', label: 'Cardiac cine MR',     modality: 'MR (cine)',        frames: 16, w: 256, h: 256, bits: 8,  pics: 4 },
  { id: 'CINE_XA',     label: 'XA coronary angio',   modality: 'XA (cine)',        frames: 12, w: 512, h: 512, bits: 8,  pics: 8 },
  { id: 'CINE_NM',     label: 'NM gated heart',      modality: 'Nuclear medicine', frames: 13, w: 64,  h: 64,  bits: 16, pics: 4 },
  { id: 'CINE_EMR',    label: 'Enhanced MR',         modality: 'MR (volumetric)',  frames: 10, w: 64,  h: 64,  bits: 16, pics: 4 },
  { id: 'CINE_ECT',    label: 'Enhanced CT',         modality: 'CT (volumetric)',  frames: 2,  w: 512, h: 512, bits: 16, pics: 4 },
];

// A representative subset for quick cine smoke runs (headless CI). The cardiac
// cine (16 frames, 256×256) is the meatiest small dataset.
export const QUICK_CINE_IDS = ['CINE_MRCARD'];

// Frame image name for a cine dataset frame — MUST match the <id>_f%03d naming
// used by cmd/mic-compress and cmd/mic-refgen.
export const cineFrameName = (id, i) => `${id}_f${String(i).padStart(3, '0')}`;

// Expand a cine dataset into its per-frame single-frame image descriptors (same
// shape as IMAGES entries) so the runner can treat each frame as an image.
export function cineFrameImages(ds) {
  return Array.from({ length: ds.frames }, (_, i) => ({
    name: cineFrameName(ds.id, i),
    modality: ds.modality,
    w: ds.w,
    h: ds.h,
    cine: ds.id,
    frameIndex: i,
  }));
}

// ---------------------------------------------------------------------------
// Codec registry — declarative descriptor per codec column. Single source of
// truth shared by the Node script and the dashboard so a codec added in one
// place cannot silently be missing from the other.
//
//   kind: 'mic'  — single-threaded MIC FSE variant; file is testdata/<name><suffix>.mic
//   kind: 'pics' — PICS parallel strips; file is testdata/<name><suffix>.mic
//                  (falls back to <suffix4> if the primary strip count is absent)
//   kind: 'wasm' — reference codec decoded via vendored WASM in the browser;
//                  file is testdata/<name>.<ext>; sizes/roundtrip in
//                  refcodecs-manifest.json under manifestKey. The Node script
//                  has no WASM adapters and treats these as informational rows.
// ---------------------------------------------------------------------------
export const CODEC_REGISTRY = [
  { id: 'mic-1state', label: 'MIC-1state',            kind: 'mic',  suffix: '' },
  { id: 'mic-4state', label: 'MIC-4state',            kind: 'mic',  suffix: '_4s' },
  { id: 'mic-8state', label: 'MIC-8state',            kind: 'mic',  suffix: '_8s' },
  // Go codec compiled to WASM, decoding the same 4-state stream as MIC-4state
  // (pure-JS vs Go/WASM head-to-head). Browser-only: the Node script skips
  // kind 'micwasm' (its loader needs the DOM), so this row is dashboard-only.
  { id: 'mic-wasm',   label: 'MIC-WASM (Go, 4-state)', kind: 'micwasm', suffix: '_4s' },
  // Pure-C codec (ojph/mic_decompress_c.c) compiled to WASM (~20 KB, no runtime).
  // Decodes the same 4-state / 8-state streams — a three-way JS vs Go/WASM vs
  // C/WASM comparison on identical bytes. Browser-only (needs the DOM loader).
  { id: 'mic-c-wasm-4', label: 'MIC-C-WASM (4-state)', kind: 'miccwasm', suffix: '_4s', state: 4 },
  { id: 'mic-c-wasm-8', label: 'MIC-C-WASM (8-state)', kind: 'miccwasm', suffix: '_8s', state: 8 },
  { id: 'pics-4',     label: 'MIC-PICS (4 strips)',   kind: 'pics', suffix: '_pics4' },
  { id: 'pics-8',     label: 'MIC-PICS (8 strips)',   kind: 'pics', suffix: '_pics8' },
  // Pure-C PICS (ojph/mic_parallel.c, pthreads) compiled to WASM — the C
  // scheduler + C inner decoder fanning out to pthread Web Workers. Runs off the
  // main thread (blocking joins). Browser-only. Only for images shipped with an
  // 8-strip PICS file (CR/MG*/DX_HAND).
  { id: 'pics-c-wasm-8', label: 'MIC-C-WASM-PICS (8 strips)', kind: 'picscwasm', suffix: '_pics8' },
  { id: 'htj2k',      label: 'HTJ2K',                 kind: 'wasm', ext: 'jph', manifestKey: 'htj2k' },
  { id: 'jpegls',     label: 'JPEG-LS',               kind: 'wasm', ext: 'jls', manifestKey: 'jpegls' },
  { id: 'jxl',        label: 'JPEG-XL',               kind: 'wasm', ext: 'jxl', manifestKey: 'jxl' },
];

// ---------------------------------------------------------------------------
// Reference-codec native-C decode throughput (MB/s of decompressed output) on
// the paper's Apple M4 Pro reference machine. Used ONLY as an informational
// fallback for codecs that don't decode live on the current runtime (the Node
// script always uses this; the dashboard uses it only when a WASM adapter's
// liveDecodeSupported is false). Compression ratios here are a fallback too,
// used only when refcodecs-manifest.json (real measured bytes) is absent.
// Source: results/20260622-225015/paper-tables.txt (Table 1, Table 4/5).
// ---------------------------------------------------------------------------
export const REFERENCE_NATIVE = {
  htj2k: {
    label: 'HTJ2K',
    ratio:      { MR: 2.38, CT: 1.77, CR: 3.77, MG1: 8.25, MG2: 8.24, MG3: 2.22, DX_HAND: 2.37, PET1: 3.02 },
    decompMBps: { MR: 328,  CT: 299,  CR: 316,  MG1: 612,  MG2: 622,  MG3: 314,  DX_HAND: 316,  PET1: 377 },
  },
  jpegls: {
    label: 'JPEG-LS',
    ratio:      { MR: 2.52, CT: 2.68, CR: 3.96, MG1: 8.91, MG2: 8.90, MG3: 2.38, DX_HAND: 2.48, PET1: 3.21 },
    decompMBps: { MR: 137,  CT: 158,  CR: 181,  MG1: 481,  MG2: 492,  MG3: 175,  DX_HAND: 147,  PET1: 197 },
  },
  // JPEG-XL native-C decode throughput (M2 Max reference; see docs/jxl-comparison.md).
  // Ratios here are a coarse fallback only — mic-refgen's real .jxl bytes are preferred.
  jxl: {
    label: 'JPEG-XL',
    ratio:      { MR: 2.6,  CT: 3.2,  CR: 4.0,  MG1: 9.5,  MG2: 9.5,  MG3: 2.5,  DX_HAND: 2.6,  PET1: 3.6 },
    decompMBps: { MR: 45,   CT: 50,   CR: 52,   MG1: 57,   MG2: 57,   MG3: 40,   DX_HAND: 46,   PET1: 44 },
  },
};

// ---------------------------------------------------------------------------
// Study-level simulation: loading a full series, not just one image.
//
// Model:
//   - One RTT paid once (persistent HTTP/2 connection to the PACS server).
//   - Download is bandwidth-bound: total transfer = sum(bytes)*8/bandwidth.
//   - Decode-blocking (worst case, main-thread decode, no Web Workers):
//       total = rtt + totalTransfer + totalDecode
//   - Decode-pipelined (best case, decode overlaps the next download):
//       total = rtt + totalTransfer + decode(last image only), accurate
//       whenever decode-per-image << transfer-per-image.
// ---------------------------------------------------------------------------
export function simulateStudy(imageBytesList, decodeMsList, mbps, rttMs) {
  const totalBytes = imageBytesList.reduce((a, b) => a + b, 0);
  const totalTransferMs = transferMs(totalBytes, mbps);
  const totalDecodeMs = decodeMsList.reduce((a, b) => a + b, 0);
  const firstImageMs = rttMs + transferMs(imageBytesList[0], mbps) + decodeMsList[0];
  const worstCaseMs = rttMs + totalTransferMs + totalDecodeMs;
  const pipelinedMs = rttMs + totalTransferMs + decodeMsList[decodeMsList.length - 1];
  return { firstImageMs, worstCaseMs, pipelinedMs, totalBytes };
}

export const STUDIES = [
  { name: '4-view digital mammography (MG1×2 + MG2×2)', images: ['MG1', 'MG1', 'MG2', 'MG2'] },
  { name: 'CR chest series (10 exposures)', images: Array(10).fill('CR') },
  { name: 'MRI sequence (24 slices)', images: Array(24).fill('MR') },
];

// ---------------------------------------------------------------------------
// Formatting helpers (shared so the DOM report and the console report render
// identical strings a developer already knows how to read).
// ---------------------------------------------------------------------------
export const fmtMs = (ms) => (ms < 1000 ? `${ms.toFixed(1)} ms` : `${(ms / 1000).toFixed(2)} s`);
export const fmtKB = (b) => `${(b / 1024).toFixed(0)} KB`;
export const fmtRatio = (r) => `${r.toFixed(2)}x`;

// Total time-to-display for one image over one network profile:
// one RTT + bandwidth-bound transfer of the compressed bytes + decode.
export function timeToDisplayMs(compressedBytes, decodeMs, profile) {
  return profile.rttMs + transferMs(compressedBytes, profile.mbps) + decodeMs;
}

// ---------------------------------------------------------------------------
// FNV-1a 32-bit checksum over a byte buffer. MUST stay bit-identical to the Go
// implementation in cmd/mic-compress (manifest.json) so the browser can verify
// decoded pixels against the manifest without shipping raw images. Returns an
// unsigned 32-bit integer; format as hex "fnv1a32:xxxxxxxx" to compare.
// ---------------------------------------------------------------------------
export function fnv1a32(bytes) {
  let h = 0x811c9dc5 >>> 0;
  for (let i = 0; i < bytes.length; i++) {
    h ^= bytes[i];
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h >>> 0;
}

export function fnv1a32Hex(bytes) {
  return 'fnv1a32:' + fnv1a32(bytes).toString(16).padStart(8, '0');
}
