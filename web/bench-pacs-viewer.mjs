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
import {
  NETWORK_PROFILES, IMAGES, CODEC_REGISTRY, REFERENCE_NATIVE,
  transferMs, simulateStudy, STUDIES, fmtMs, fmtKB,
} from './pacs-model.mjs';

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

// NETWORK_PROFILES, transferMs, IMAGES, CODEC_REGISTRY, REFERENCE_NATIVE,
// simulateStudy, STUDIES, fmtMs, fmtKB now come from ./pacs-model.mjs (the
// single source of truth shared with the browser dashboard).

// Try to read real reference-codec sizes produced by `mic-refgen`; falls back
// to the native ratio table when the manifest is absent (no cgo build run).
function loadRefManifest() {
  const p = resolve(__dir, 'testdata/refcodecs-manifest.json');
  if (!existsSync(p)) return null;
  try { return JSON.parse(readFileSync(p, 'utf8')); } catch { return null; }
}
const REF_MANIFEST = loadRefManifest();

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

    for (const codec of CODEC_REGISTRY) {
      if (codec.kind === 'mic') {
        const path = resolve(__dir, `testdata/${img.name}${codec.suffix}.mic`);
        if (!existsSync(path)) continue;
        const bytes = new Uint8Array(readFileSync(path));
        const { medianMs } = timeDecode(bytes);
        codecs.push({ label: codec.label, compressedBytes: bytes.length, decodeMs: medianMs, reference: false });

      } else if (codec.kind === 'pics') {
        // Load only this codec's own strip file (see candidatePaths in
        // pacs-runner.mjs) — no cross-strip-count fallback.
        const path = resolve(__dir, `testdata/${img.name}${codec.suffix}.mic`);
        if (!existsSync(path)) continue;
        const bytes = new Uint8Array(readFileSync(path));
        const hdr = MICDecoder.parsePICSHeader(bytes);
        const workers = Math.min(hdr.numStrips, availableWorkers);
        const { medianMs } = await timePICSDecode(bytes, workers);
        codecs.push({
          label: `${codec.label} [${workers}w]`, compressedBytes: bytes.length,
          decodeMs: medianMs, reference: false,
        });

      } else if (codec.kind === 'wasm') {
        // Reference codec: no browser/WASM decoder in the Node script (that's
        // the dashboard's job). Prefer the real .jph/.jls/.jxl size measured by
        // `mic-refgen` (refcodecs-manifest.json) when present; else fall back to
        // the paper's native ratio table. Decode ms is always the native-C
        // reference throughput (informational, not a live browser measurement).
        const ref = REFERENCE_NATIVE[codec.manifestKey];
        if (!ref) continue;
        const mbps = ref.decompMBps[img.name];
        if (mbps == null) continue;
        const manifestEntry = REF_MANIFEST?.images?.[img.name]?.[codec.manifestKey];
        let compressedBytes;
        if (manifestEntry?.bytes != null) {
          compressedBytes = manifestEntry.bytes;                 // real measured file
        } else if (ref.ratio[img.name] != null) {
          compressedBytes = Math.round(rawBytes / ref.ratio[img.name]); // fallback
        } else {
          continue;
        }
        const outputMB = rawBytes / (1024 * 1024);
        const decodeMs = (outputMB / mbps) * 1000;
        codecs.push({
          label: `${codec.label} (native C, M4 Pro — no browser decoder)`,
          compressedBytes, decodeMs, reference: true,
        });
      }
    }

    perImage[img.name] = { ...img, rawBytes, codecs };
  }
  return perImage;
}

// ---------------------------------------------------------------------------
// Formatting (fmtMs/fmtKB come from pacs-model.mjs; pad helpers are Node-only)
// ---------------------------------------------------------------------------
const pad = (s, w) => String(s).padStart(w);
const padL = (s, w) => String(s).padEnd(w);

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
//     simulateStudy() and STUDIES now come from ./pacs-model.mjs.
// ---------------------------------------------------------------------------
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
