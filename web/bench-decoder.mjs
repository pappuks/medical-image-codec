// Benchmark for the MIC JavaScript decoder (mic-decoder.js).
// Run: node bench-decoder.mjs [--iterations N] [--workers W] [--no-parallel]
//
// Reports decompression throughput in MB/s (compressed input) and MP/s
// (output megapixels per second) for each test image, averaged over N runs.
//
// Parallel section: benchmarks PICS files decoded with worker_threads,
// sweeping 1/2/4/N workers and reporting speedup vs single-threaded.
//
// WASM decoder (mic-decoder-wasm.js) requires a browser environment and is
// not benchmarked here.  Run the browser demo in index.html and use DevTools
// performance profiling for WASM measurements.

import { MICDecoder } from './mic-decoder.js';
import { readFileSync, existsSync } from 'fs';
import { performance } from 'perf_hooks';
import { Worker } from 'node:worker_threads';
import { cpus } from 'node:os';
import { fileURLToPath } from 'node:url';
import { resolve, dirname } from 'node:path';

const __dir = dirname(fileURLToPath(import.meta.url));

// ---------------------------------------------------------------------------
// CLI args
// ---------------------------------------------------------------------------
const args = process.argv.slice(2);
const iterIdx   = args.indexOf('--iterations');
const workerIdx = args.indexOf('--workers');
const ITERATIONS  = iterIdx   !== -1 ? parseInt(args[iterIdx   + 1], 10) : 20;
const MAX_WORKERS = workerIdx !== -1 ? parseInt(args[workerIdx + 1], 10) : Math.min(cpus().length, 16);
const NO_PARALLEL = args.includes('--no-parallel');
const WARMUP = 3; // warm-up runs excluded from timing

// ---------------------------------------------------------------------------
// Test images — single-threaded
// ---------------------------------------------------------------------------
const FILES = [
  // Standard 1-state FSE
  { mic: 'testdata/MR.mic',   name: 'MR  256×256   (1-state)' },
  { mic: 'testdata/CT.mic',   name: 'CT  512×512   (1-state)' },
  { mic: 'testdata/CR.mic',   name: 'CR  1760×2140 (1-state)' },
  { mic: 'testdata/MG1.mic',  name: 'MG1 1996×2457 (1-state)' },
  { mic: 'testdata/MG2.mic',  name: 'MG2 1996×2457 (1-state)' },
  { mic: 'testdata/MG3.mic',  name: 'MG3 3064×4774 (1-state)' },
  // 4-state FSE
  { mic: 'testdata/MR_4s.mic',  name: 'MR  256×256   (4-state)' },
  { mic: 'testdata/CT_4s.mic',  name: 'CT  512×512   (4-state)' },
  { mic: 'testdata/CR_4s.mic',  name: 'CR  1760×2140 (4-state)' },
  { mic: 'testdata/MG1_4s.mic', name: 'MG1 1996×2457 (4-state)' },
  { mic: 'testdata/MG2_4s.mic', name: 'MG2 1996×2457 (4-state)' },
  { mic: 'testdata/MG3_4s.mic', name: 'MG3 3064×4774 (4-state)' },
  // PICS parallel strips (decoded sequentially here for baseline)
  { mic: 'testdata/MR_pics4.mic',  name: 'MR  256×256   (PICS-4 seq)' },
  { mic: 'testdata/CT_pics4.mic',  name: 'CT  512×512   (PICS-4 seq)' },
  { mic: 'testdata/CR_pics8.mic',  name: 'CR  1760×2140 (PICS-8 seq)' },
  { mic: 'testdata/MG1_pics8.mic', name: 'MG1 1996×2457 (PICS-8 seq)' },
];

// PICS files used for the parallel worker sweep
const PICS_FILES = [
  { mic: 'testdata/MR_pics4.mic',  name: 'MR  256×256',   strips: 4 },
  { mic: 'testdata/CT_pics4.mic',  name: 'CT  512×512',   strips: 4 },
  { mic: 'testdata/CR_pics8.mic',  name: 'CR  1760×2140', strips: 8 },
  { mic: 'testdata/MG1_pics8.mic', name: 'MG1 1996×2457', strips: 8 },
];

// ---------------------------------------------------------------------------
// Single-threaded benchmark runner
// ---------------------------------------------------------------------------
function bench(micBytes, name) {
  const compressedMB = micBytes.length / (1024 * 1024);

  // Warm up
  let result;
  for (let i = 0; i < WARMUP; i++) {
    result = MICDecoder.decodeFile(micBytes);
  }

  const pixelCount = result.width * result.height;
  const outputMB = (pixelCount * 2) / (1024 * 1024); // uint16

  // Timed runs
  const times = [];
  for (let i = 0; i < ITERATIONS; i++) {
    const t0 = performance.now();
    MICDecoder.decodeFile(micBytes);
    times.push(performance.now() - t0);
  }

  times.sort((a, b) => a - b);
  const median = times[Math.floor(times.length / 2)];
  const best   = times[0];
  const avg    = times.reduce((s, t) => s + t, 0) / times.length;

  return {
    name,
    compressedKB: (micBytes.length / 1024).toFixed(0),
    outputMB: outputMB.toFixed(2),
    ratio: (outputMB / compressedMB).toFixed(2),
    medianMs: median.toFixed(2),
    bestMs:   best.toFixed(2),
    avgMs:    avg.toFixed(2),
    throughputMedian: (outputMB / (median / 1000)).toFixed(0),
    throughputBest:   (outputMB / (best   / 1000)).toFixed(0),
    mpxMedian: ((pixelCount / 1e6) / (median / 1000)).toFixed(1),
    _outputMB: outputMB,
    _medianMs: median,
  };
}

// ---------------------------------------------------------------------------
// Parallel worker pool (node:worker_threads)
// ---------------------------------------------------------------------------

const WORKER_PATH = resolve(__dir, 'bench-worker.mjs');

class NodeWorkerPool {
  constructor(workerCount) {
    this._workerCount = workerCount;
    this._workers = [];
    this._pending = new Map(); // stripIndex → {resolve, reject}
  }

  async init() {
    for (let i = 0; i < this._workerCount; i++) {
      const w = new Worker(WORKER_PATH);
      w.on('message', (msg) => this._onMessage(msg));
      w.on('error', (err) => {
        for (const [, { reject }] of this._pending) reject(err);
        this._pending.clear();
      });
      this._workers.push(w);
    }
  }

  _onMessage(msg) {
    if (msg.type !== 'strip-done') return;
    const entry = this._pending.get(msg.stripIndex);
    if (!entry) return;
    this._pending.delete(msg.stripIndex);
    if (msg.error) {
      entry.reject(new Error(`strip ${msg.stripIndex}: ${msg.error}`));
    } else {
      entry.resolve(msg.pixelBuffer ?? null);
    }
  }

  async decodePICS(fileBytes) {
    const hdr = MICDecoder.parsePICSHeader(fileBytes);
    const { width, height, numStrips, stripH, strips, dataOffset } = hdr;

    // Use SharedArrayBuffer for zero-copy output (always available in Node.js).
    const fileSAB = new SharedArrayBuffer(fileBytes.byteLength);
    new Uint8Array(fileSAB).set(fileBytes);
    const outSAB = new SharedArrayBuffer(width * height * 2);

    const pixelChunks = new Array(numStrips).fill(null);

    const promises = strips.map((strip, s) => {
      const y0 = s * stripH;
      const y1 = Math.min(y0 + stripH, height);
      const sh = y1 - y0;
      const worker = this._workers[s % this._workers.length];

      return new Promise((resolve, reject) => {
        this._pending.set(s, {
          resolve: (buf) => { pixelChunks[s] = buf; resolve(); },
          reject,
        });
        worker.postMessage({
          type: 'decode-strip',
          stripIndex: s,
          fileBuffer: fileSAB,
          fileOffset: dataOffset + strip.offset,
          fileLength: strip.length,
          outBuffer: outSAB,
          outOffset: y0 * width,
          width,
          stripHeight: sh,
        });
      });
    });

    await Promise.all(promises);
    return { pixels: new Uint16Array(outSAB), width, height };
  }

  terminate() {
    for (const w of this._workers) w.terminate();
    this._workers = [];
    this._pending.clear();
  }
}

// ---------------------------------------------------------------------------
// Parallel benchmark runner
// ---------------------------------------------------------------------------
async function benchParallel(micBytes, workerCount) {
  const pool = new NodeWorkerPool(workerCount);
  await pool.init();

  const hdr = MICDecoder.parsePICSHeader(micBytes);
  const outputMB = (hdr.width * hdr.height * 2) / (1024 * 1024);

  // Warm up
  for (let i = 0; i < WARMUP; i++) await pool.decodePICS(micBytes);

  // Timed runs
  const times = [];
  for (let i = 0; i < ITERATIONS; i++) {
    const t0 = performance.now();
    await pool.decodePICS(micBytes);
    times.push(performance.now() - t0);
  }

  pool.terminate();

  times.sort((a, b) => a - b);
  const median = times[Math.floor(times.length / 2)];
  const best   = times[0];

  return {
    medianMs: median,
    bestMs:   best,
    throughputMedian: outputMB / (median / 1000),
    _outputMB: outputMB,
  };
}

// ---------------------------------------------------------------------------
// Formatting helpers
// ---------------------------------------------------------------------------
const pad  = (s, w) => String(s).padStart(w);
const padL = (s, w) => String(s).padEnd(w);

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
console.log(`MIC JS Decoder Benchmark  —  ${ITERATIONS} iterations (+ ${WARMUP} warm-up) per file`);
console.log(`Node ${process.version}  |  CPUs: ${cpus().length}  |  Max workers: ${MAX_WORKERS}`);
console.log(`${new Date().toISOString()}\n`);

// ── Single-threaded ────────────────────────────────────────────────────────
console.log('── Single-threaded ─────────────────────────────────────────────');
const results = [];
let skipped = 0;

for (const f of FILES) {
  if (!existsSync(f.mic)) {
    console.log(`  skip  ${f.name}  (${f.mic} not found)`);
    skipped++;
    continue;
  }
  const bytes = new Uint8Array(readFileSync(f.mic));
  process.stdout.write(`  bench ${f.name} ... `);
  try {
    const r = bench(bytes, f.name);
    results.push(r);
    console.log(`median ${r.medianMs} ms  (${r.throughputMedian} MB/s, ${r.mpxMedian} MP/s)`);
  } catch (e) {
    console.log(`FAIL: ${e.message}`);
  }
}

if (results.length > 0) {
  const W = {
    name: Math.max(28, ...results.map(r => r.name.length)),
    comp: 10, out: 8, ratio: 6, med: 9, best: 9, avg: 9, mbps: 10, mpx: 9,
  };
  const hr = '-'.repeat(W.name + W.comp + W.out + W.ratio + W.med + W.best + W.avg + W.mbps + W.mpx + 8 * 3 + 1);

  console.log(`\n${'='.repeat(hr.length)}`);
  console.log('SINGLE-THREADED SUMMARY');
  console.log(hr);
  console.log(
    padL('Image', W.name) + ' | ' +
    pad('Comp KB', W.comp) + ' | ' +
    pad('Out MB', W.out)   + ' | ' +
    pad('Ratio', W.ratio)  + ' | ' +
    pad('Median ms', W.med) + ' | ' +
    pad('Best ms', W.best) + ' | ' +
    pad('Avg ms', W.avg)   + ' | ' +
    pad('MB/s (med)', W.mbps) + ' | ' +
    pad('MP/s (med)', W.mpx)
  );
  console.log(hr);
  for (const r of results) {
    console.log(
      padL(r.name, W.name) + ' | ' +
      pad(r.compressedKB, W.comp) + ' | ' +
      pad(r.outputMB, W.out) + ' | ' +
      pad(r.ratio, W.ratio) + ' | ' +
      pad(r.medianMs, W.med) + ' | ' +
      pad(r.bestMs, W.best) + ' | ' +
      pad(r.avgMs, W.avg)   + ' | ' +
      pad(r.throughputMedian, W.mbps) + ' | ' +
      pad(r.mpxMedian, W.mpx)
    );
  }
  console.log(hr);

  const groups = {};
  for (const r of results) {
    const mod = r.name.split(' ')[0];
    if (!groups[mod]) groups[mod] = [];
    groups[mod].push(parseFloat(r.throughputMedian));
  }
  console.log('\nPeak throughput by modality (best variant):');
  for (const [mod, vals] of Object.entries(groups)) {
    console.log(`  ${mod.padEnd(4)}  ${Math.max(...vals)} MB/s`);
  }
}

// ── Parallel worker sweep ──────────────────────────────────────────────────
if (!NO_PARALLEL) {
  const picsAvailable = PICS_FILES.filter(f => existsSync(f.mic));

  if (picsAvailable.length === 0) {
    console.log('\n── Parallel workers ───────────────────────────────────────────');
    console.log('  No PICS files found. Generate them with:');
    console.log('    go run ./cmd/mic-compress/ -testdata');
  } else {
    // Worker counts to sweep: 1, 2, 4, … up to MAX_WORKERS, always include MAX_WORKERS
    const workerCounts = [...new Set(
      [1, 2, 4, 8, MAX_WORKERS].filter(n => n >= 1 && n <= MAX_WORKERS)
    )].sort((a, b) => a - b);

    console.log(`\n── Parallel workers (worker_threads, SharedArrayBuffer, workers: ${workerCounts.join('/')}) ──`);

    for (const f of picsAvailable) {
      const bytes = new Uint8Array(readFileSync(f.mic));
      console.log(`\n  ${f.name}  (${f.strips} strips)`);

      let baselineMs = null;
      const rows = [];

      for (const wc of workerCounts) {
        // Cap workers at number of strips — extra workers are idle
        const effective = Math.min(wc, f.strips);
        process.stdout.write(`    workers=${wc} (effective ${effective}) ... `);
        try {
          const r = await benchParallel(bytes, effective);
          if (baselineMs === null) baselineMs = r.medianMs;
          const speedup = (baselineMs / r.medianMs).toFixed(2);
          rows.push({ wc, effective, ...r, speedup });
          console.log(
            `median ${r.medianMs.toFixed(2)} ms  ` +
            `(${r.throughputMedian.toFixed(0)} MB/s,  ${speedup}× vs 1-worker)`
          );
        } catch (e) {
          console.log(`FAIL: ${e.message}`);
        }
      }

      if (rows.length > 1) {
        const best = rows.reduce((a, b) => a.throughputMedian > b.throughputMedian ? a : b);
        console.log(`    → best: ${best.wc} workers → ${best.throughputMedian.toFixed(0)} MB/s (${best.speedup}×)`);
      }
    }
  }
}

if (skipped > 0) {
  console.log(`\n${skipped} single-threaded file(s) skipped. Run 'go run ./cmd/mic-compress/ -testdata' to generate them.`);
}

if (results.length === 0 && (NO_PARALLEL || PICS_FILES.every(f => !existsSync(f.mic)))) {
  console.log('\nNo test files found. Run the Go compressor first:');
  console.log('  go run ./cmd/mic-compress/ -testdata');
  process.exit(0);
}
