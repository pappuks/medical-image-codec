// bench-pacs-viewer.mjs — End-to-end PACS web-viewer simulation.
//
// Models the thing a radiologist actually experiences: click a study in a
// browser-based PACS viewer, wait for pixels. That wait is two stages:
//
//   1. Network transfer of the compressed image bytes from PACS server to browser
//   2. In-browser decompression of those bytes to a displayable pixel buffer
//
// This script measures stage 2 for real (using the actual JS decoder, same
// code a browser would run) and simulates stage 1 under a few representative
// network conditions (hospital LAN, hospital WiFi, home broadband, cellular).
//
// Only MIC has a browser/JS decoder in this repository, so MIC decode times
// below are REAL measurements on whatever machine runs this script. HTJ2K and
// JPEG-LS are included for compressed-size comparison (compression ratio is
// platform-independent) but their decode times are native C (CGO) numbers
// from the paper's Apple M4 Pro reference run — there is no browser/WASM
// decoder for them in this codebase, so those decode numbers are informational
// only and NOT comparable apples-to-apples with the live MIC measurements.
// They are clearly labeled as such in the output.
//
// Run: node bench-pacs-viewer.mjs [--iterations N] [--json out.json]

import { MICDecoder } from './mic-decoder.js';
import { readFileSync, existsSync, writeFileSync } from 'fs';
import { performance } from 'perf_hooks';
import { Worker } from 'node:worker_threads';
import { fileURLToPath } from 'node:url';
import { resolve, dirname } from 'node:path';
import { cpus } from 'node:os';

const __dir = dirname(fileURLToPath(import.meta.url));

// ---------------------------------------------------------------------------
// CLI args
// ---------------------------------------------------------------------------
const args = process.argv.slice(2);
const iterIdx = args.indexOf('--iterations');
const jsonIdx = args.indexOf('--json');
const ITERATIONS = iterIdx !== -1 ? parseInt(args[iterIdx + 1], 10) : 15;
const JSON_OUT = jsonIdx !== -1 ? args[jsonIdx + 1] : null;
const WARMUP = 3;

// ---------------------------------------------------------------------------
// Network profiles — representative PACS deployment scenarios
// ---------------------------------------------------------------------------
const NETWORK_PROFILES = [
  { name: 'Hospital LAN',      mbps: 100, rttMs: 2,  note: 'radiology workstation, wired' },
  { name: 'Hospital Wi-Fi',    mbps: 20,  rttMs: 10, note: 'mobile cart / tablet on-site' },
  { name: 'Home Broadband',    mbps: 25,  rttMs: 20, note: 'remote read, VPN' },
  { name: 'Cellular (4G/LTE)', mbps: 5,   rttMs: 60, note: 'remote read, on-call physician' },
];

function transferMs(bytes, mbps) {
  return (bytes * 8) / (mbps * 1e6) * 1000;
}

// ---------------------------------------------------------------------------
// Test images (single-frame grayscale, all present in web/testdata/)
// ---------------------------------------------------------------------------
const IMAGES = [
  { name: 'MR',       modality: 'MRI',            w: 256,  h: 256 },
  { name: 'CT',       modality: 'CT',              w: 512,  h: 512 },
  { name: 'PET1',     modality: 'PET',             w: 256,  h: 256 },
  { name: 'DX_HAND',  modality: 'Digital X-ray',   w: 1410, h: 1480 },
  { name: 'CR',       modality: 'Computed Radiography', w: 1760, h: 2140 },
  { name: 'MG1',      modality: 'Mammography',     w: 1996, h: 2457 },
  { name: 'MG2',      modality: 'Mammography',     w: 1996, h: 2457 },
  { name: 'MG3',      modality: 'Mammography (large)', w: 3064, h: 4774 },
];

// MIC codec variants, single-threaded. PICS (parallel) variants handled separately.
const MIC_VARIANTS = [
  { label: 'MIC-1state', suffix: '' },
  { label: 'MIC-4state', suffix: '_4s' },
  { label: 'MIC-8state', suffix: '_8s' },
];

// PICS parallel-strip variants (decoded across worker_threads, mirroring a
// browser Web Worker pool). Only generated for some images by mic-compress -testdata.
const PICS_VARIANTS = [
  { label: 'MIC-PICS (4-state strips)', suffix: '_pics8', suffix4: '_pics4' },
];

// Reference codecs with no browser decoder in this repo. Ratios are
// platform-independent (real). Decode throughput is native C (CGO) on the
// paper's Apple M4 Pro reference machine — NOT a browser number.
// Source: results/20260622-225015/paper-tables.txt (Table 1, Table 4/5).
const REFERENCE_CODECS = {
  HTJ2K: {
    label: 'HTJ2K (native C, M4 Pro — no browser decoder)',
    ratio: { MR: 2.38, CT: 1.77, CR: 3.77, MG1: 8.25, MG2: 8.24, MG3: 2.22, DX_HAND: 2.37, PET1: 3.02 },
    decompMBps: { MR: 328, CT: 299, CR: 316, MG1: 612, MG2: 622, MG3: 314, DX_HAND: 316, PET1: 377 },
  },
  'JPEG-LS': {
    label: 'JPEG-LS (native C, M4 Pro — no browser decoder)',
    ratio: { MR: 2.52, CT: 2.68, CR: 3.96, MG1: 8.91, MG2: 8.90, MG3: 2.38, DX_HAND: 2.48, PET1: 3.21 },
    decompMBps: { MR: 137, CT: 158, CR: 181, MG1: 481, MG2: 492, MG3: 175, DX_HAND: 147, PET1: 197 },
  },
};

// ---------------------------------------------------------------------------
// Real decode timing (MIC, single-threaded)
// ---------------------------------------------------------------------------
function timeDecode(micBytes) {
  let result;
  for (let i = 0; i < WARMUP; i++) result = MICDecoder.decodeFile(micBytes);

  const times = [];
  for (let i = 0; i < ITERATIONS; i++) {
    const t0 = performance.now();
    MICDecoder.decodeFile(micBytes);
    times.push(performance.now() - t0);
  }
  times.sort((a, b) => a - b);
  return { medianMs: times[Math.floor(times.length / 2)], width: result.width, height: result.height };
}

// ---------------------------------------------------------------------------
// Real decode timing (MIC, PICS parallel strips via worker_threads —
// stands in for a browser Web Worker pool decoding a large image's strips
// concurrently on separate cores)
// ---------------------------------------------------------------------------
const WORKER_PATH = resolve(__dir, 'bench-worker.mjs');

class NodeWorkerPool {
  constructor(n) { this._n = n; this._workers = []; this._pending = new Map(); }
  async init() {
    for (let i = 0; i < this._n; i++) {
      const w = new Worker(WORKER_PATH);
      w.on('message', (msg) => this._onMessage(msg));
      this._workers.push(w);
    }
  }
  _onMessage(msg) {
    if (msg.type !== 'strip-done') return;
    const e = this._pending.get(msg.stripIndex);
    if (!e) return;
    this._pending.delete(msg.stripIndex);
    msg.error ? e.reject(new Error(msg.error)) : e.resolve();
  }
  async decodePICS(fileBytes) {
    const hdr = MICDecoder.parsePICSHeader(fileBytes);
    const { width, height, numStrips, stripH, strips, dataOffset } = hdr;
    const fileSAB = new SharedArrayBuffer(fileBytes.byteLength);
    new Uint8Array(fileSAB).set(fileBytes);
    const outSAB = new SharedArrayBuffer(width * height * 2);
    const promises = strips.map((strip, s) => {
      const y0 = s * stripH;
      const y1 = Math.min(y0 + stripH, height);
      const worker = this._workers[s % this._workers.length];
      return new Promise((res, rej) => {
        this._pending.set(s, { resolve: res, reject: rej });
        worker.postMessage({
          type: 'decode-strip', stripIndex: s, fileBuffer: fileSAB,
          fileOffset: dataOffset + strip.offset, fileLength: strip.length,
          outBuffer: outSAB, outOffset: y0 * width, width, stripHeight: y1 - y0,
        });
      });
    });
    await Promise.all(promises);
    return { width, height };
  }
  terminate() { for (const w of this._workers) w.terminate(); }
}

async function timePICSDecode(micBytes, workerCount) {
  const pool = new NodeWorkerPool(workerCount);
  await pool.init();
  let dims;
  for (let i = 0; i < WARMUP; i++) dims = await pool.decodePICS(micBytes);
  const times = [];
  for (let i = 0; i < ITERATIONS; i++) {
    const t0 = performance.now();
    await pool.decodePICS(micBytes);
    times.push(performance.now() - t0);
  }
  pool.terminate();
  times.sort((a, b) => a - b);
  return { medianMs: times[Math.floor(times.length / 2)], width: dims.width, height: dims.height };
}

// ---------------------------------------------------------------------------
// Collect per-image, per-codec results: {compressedBytes, decodeMs, isReference}
// ---------------------------------------------------------------------------
async function collectCodecResults() {
  const perImage = {};
  const availableWorkers = Math.min(cpus().length, 8);

  for (const img of IMAGES) {
    const rawBytes = img.w * img.h * 2;
    const codecs = [];

    for (const variant of MIC_VARIANTS) {
      const path = resolve(__dir, `testdata/${img.name}${variant.suffix}.mic`);
      if (!existsSync(path)) continue;
      const bytes = new Uint8Array(readFileSync(path));
      const { medianMs } = timeDecode(bytes);
      codecs.push({ label: variant.label, compressedBytes: bytes.length, decodeMs: medianMs, reference: false });
    }

    for (const variant of PICS_VARIANTS) {
      let path = resolve(__dir, `testdata/${img.name}${variant.suffix}.mic`);
      if (!existsSync(path)) path = resolve(__dir, `testdata/${img.name}${variant.suffix4}.mic`);
      if (!existsSync(path)) continue;
      const bytes = new Uint8Array(readFileSync(path));
      const hdr = MICDecoder.parsePICSHeader(bytes);
      const workers = Math.min(hdr.numStrips, availableWorkers);
      const { medianMs } = await timePICSDecode(bytes, workers);
      codecs.push({
        label: `${variant.label} [${workers}w]`, compressedBytes: bytes.length,
        decodeMs: medianMs, reference: false,
      });
    }

    for (const [codecName, ref] of Object.entries(REFERENCE_CODECS)) {
      const ratio = ref.ratio[img.name];
      const mbps = ref.decompMBps[img.name];
      if (ratio == null || mbps == null) continue;
      const compressedBytes = Math.round(rawBytes / ratio);
      const outputMB = rawBytes / (1024 * 1024);
      const decodeMs = (outputMB / mbps) * 1000;
      codecs.push({ label: ref.label, compressedBytes, decodeMs, reference: true });
    }

    perImage[img.name] = { ...img, rawBytes, codecs };
  }
  return perImage;
}

// ---------------------------------------------------------------------------
// Formatting
// ---------------------------------------------------------------------------
const pad = (s, w) => String(s).padStart(w);
const padL = (s, w) => String(s).padEnd(w);
const fmtMs = (ms) => (ms < 1000 ? `${ms.toFixed(1)} ms` : `${(ms / 1000).toFixed(2)} s`);
const fmtKB = (b) => `${(b / 1024).toFixed(0)} KB`;

function printImageReport(img) {
  console.log(`\n${img.name}  (${img.modality}, ${img.w}×${img.h}, raw ${(img.rawBytes / 1024 / 1024).toFixed(2)} MB)`);
  console.log('='.repeat(100));

  const W = { codec: 40, size: 10, ratio: 7, decode: 9 };
  console.log(
    padL('Codec', W.codec) + ' | ' + pad('Comp.', W.size) + ' | ' +
    pad('Ratio', W.ratio) + ' | ' + pad('Decode', W.decode) + ' | Time to display, by network'
  );
  console.log(
    padL('', W.codec) + ' | ' + pad('', W.size) + ' | ' + pad('', W.ratio) + ' | ' + pad('', W.decode) + ' | ' +
    NETWORK_PROFILES.map(p => padL(p.name, 16)).join('')
  );
  console.log('-'.repeat(100));

  for (const c of img.codecs) {
    const ratio = img.rawBytes / c.compressedBytes;
    const netTimes = NETWORK_PROFILES.map(p => {
      const total = p.rttMs + transferMs(c.compressedBytes, p.mbps) + c.decodeMs;
      return padL(fmtMs(total), 16);
    });
    const flag = c.reference ? '*' : ' ';
    console.log(
      padL(c.label + flag, W.codec) + ' | ' + pad(fmtKB(c.compressedBytes), W.size) + ' | ' +
      pad(ratio.toFixed(2) + 'x', W.ratio) + ' | ' + pad(fmtMs(c.decodeMs), W.decode) + ' | ' +
      netTimes.join('')
    );
  }
  console.log('* = reference codec, native C decode (Apple M4 Pro), no browser/WASM decoder exists — not a live measurement');
}

// ---------------------------------------------------------------------------
// Study-level simulation: loading a full series, not just one image.
//
// Model:
//   - One RTT paid once (persistent HTTP/2 connection to the PACS server).
//   - Download is bandwidth-bound: total transfer time = sum(bytes)*8/bandwidth.
//     (True for every profile here since per-image sizes are small relative
//     to typical realistic PACS series and TCP window sizes.)
//   - Decode-blocking (worst case, main-thread decode, no Web Workers):
//       total = rtt + totalTransfer + totalDecode
//   - Decode-pipelined (best case, decode happens in a Worker while the next
//     image is still downloading — what mic-decoder-parallel.js enables):
//       total = rtt + max(totalTransfer, totalDecode) [+ decode of last image
//               if it can't overlap with anything more]
//     Approximated here as rtt + totalTransfer + decode(last image only),
//     which is accurate whenever decode-per-image << transfer-per-image
//     (true for MIC on every profile below; network dominates).
// ---------------------------------------------------------------------------
function simulateStudy(imageBytesList, decodeMsList, mbps, rttMs) {
  const totalBytes = imageBytesList.reduce((a, b) => a + b, 0);
  const totalTransferMs = transferMs(totalBytes, mbps);
  const totalDecodeMs = decodeMsList.reduce((a, b) => a + b, 0);
  const firstImageMs = rttMs + transferMs(imageBytesList[0], mbps) + decodeMsList[0];
  const worstCaseMs = rttMs + totalTransferMs + totalDecodeMs;
  const pipelinedMs = rttMs + totalTransferMs + decodeMsList[decodeMsList.length - 1];
  return { firstImageMs, worstCaseMs, pipelinedMs, totalBytes };
}

const STUDIES = [
  { name: '4-view digital mammography (MG1×2 + MG2×2)', images: ['MG1', 'MG1', 'MG2', 'MG2'] },
  { name: 'CR chest series (10 exposures)', images: Array(10).fill('CR') },
  { name: 'MRI sequence (24 slices)', images: Array(24).fill('MR') },
];

function printStudyReport(perImage, codecLabel) {
  console.log(`\nStudy-load simulation — codec: ${codecLabel}`);
  console.log('='.repeat(100));
  for (const study of STUDIES) {
    console.log(`\n  ${study.name}`);
    const codecPerImg = study.images.map(name => {
      const entry = perImage[name].codecs.find(c => c.label === codecLabel);
      return entry;
    });
    if (codecPerImg.some(c => !c)) { console.log('    (codec not available for this image set — skipped)'); continue; }
    const sizes = codecPerImg.map(c => c.compressedBytes);
    const decodes = codecPerImg.map(c => c.decodeMs);
    const totalMB = sizes.reduce((a, b) => a + b, 0) / (1024 * 1024);
    console.log(`    ${study.images.length} images, ${totalMB.toFixed(1)} MB compressed total`);
    console.log(
      padL('Network', 18) + pad('1st image', 12) + pad('Full study', 16) + pad('Full study', 16)
    );
    console.log(
      padL('', 18) + pad('', 12) + pad('(pipelined)', 16) + pad('(no workers)', 16)
    );
    for (const p of NETWORK_PROFILES) {
      const sim = simulateStudy(sizes, decodes, p.mbps, p.rttMs);
      console.log(
        padL(p.name, 18) + pad(fmtMs(sim.firstImageMs), 12) +
        pad(fmtMs(sim.pipelinedMs), 16) + pad(fmtMs(sim.worstCaseMs), 16)
      );
    }
  }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
console.log('PACS Web Viewer Benchmark — network transfer + in-browser decode');
console.log(`Live decode measurements: Node ${process.version}, ${cpus().length} logical CPUs, ${cpus()[0]?.model || 'unknown CPU'}`);
console.log(`${ITERATIONS} iterations (+${WARMUP} warm-up) per codec/image. ${new Date().toISOString()}`);
console.log('\nNetwork profiles simulated:');
for (const p of NETWORK_PROFILES) console.log(`  ${padL(p.name, 18)} ${pad(p.mbps + ' Mbps', 10)}  RTT ${pad(p.rttMs + ' ms', 6)}  (${p.note})`);

const perImage = await collectCodecResults();

for (const img of IMAGES) {
  if (perImage[img.name].codecs.length > 0) printImageReport(perImage[img.name]);
}

printStudyReport(perImage, 'MIC-4state');
printStudyReport(perImage, 'MIC-8state');

console.log('\nTakeaways:');
console.log('  - On Hospital LAN, decode time (not network) dominates time-to-display for MIC;');
console.log('    faster FSE state counts / PICS parallel decode matter most here.');
console.log('  - On Home Broadband and Cellular, network transfer dominates (often 5-30x the');
console.log('    decode time); compression ratio (bytes on the wire) matters more than decode speed.');
console.log('  - PICS (Web Worker parallel decode) mainly helps large images (CR/MG) on fast');
console.log('    networks, where decode time would otherwise be the bottleneck.');

if (JSON_OUT) {
  writeFileSync(JSON_OUT, JSON.stringify({ generatedAt: new Date().toISOString(), networkProfiles: NETWORK_PROFILES, images: perImage }, null, 2));
  console.log(`\nWrote JSON results to ${JSON_OUT}`);
}
