// mic-decoder-parallel.js — Parallel PICS strip decoder using Web Workers.
//
// Uses SharedArrayBuffer when crossOriginIsolated is true (requires the page to
// be served with COOP + COEP headers — see serve.json).  Falls back to
// transferable ArrayBuffers otherwise.
//
// Usage:
//   import { createPICSDecoder } from './mic-decoder-parallel.js';
//   const decoder = await createPICSDecoder();          // creates worker pool
//   const { pixels, width, height } = await decoder.decodePICS(fileBytes);
//   decoder.terminate();                                // optional cleanup

import { MICDecoder } from './mic-decoder.js';

// ---------------------------------------------------------------------------
// Worker URL — resolved relative to this module so it works from any origin.
// ---------------------------------------------------------------------------
const WORKER_URL = new URL('./mic-worker.js', import.meta.url);

// ---------------------------------------------------------------------------
// PICSSABDecoder
// ---------------------------------------------------------------------------

export class PICSSABDecoder {
  /**
   * @param {number} workerCount  Number of workers to create.  Defaults to
   *   navigator.hardwareConcurrency (capped at 16) or 4 as a fallback.
   */
  constructor(workerCount) {
    this._workerCount = workerCount
      ?? Math.min(navigator.hardwareConcurrency ?? 4, 16);
    this._workers = [];
    this._pending = new Map(); // stripIndex → {resolve, reject}
    this._sabAvailable = typeof SharedArrayBuffer !== 'undefined' &&
                         (typeof crossOriginIsolated !== 'undefined' && crossOriginIsolated);
    this._ready = false;
  }

  /**
   * Create the worker pool and wait for all workers to be ready.
   * Called automatically by createPICSDecoder(); call manually only if you
   * construct PICSSABDecoder directly.
   * @returns {Promise<void>}
   */
  async init() {
    const readyPromises = [];

    for (let i = 0; i < this._workerCount; i++) {
      const worker = new Worker(WORKER_URL, { type: 'module' });

      // Route incoming strip-done messages to the waiting promise.
      worker.onmessage = (e) => this._onWorkerMessage(e.data);
      worker.onerror = (e) => {
        // Reject all pending strips on worker crash.
        for (const [, { reject }] of this._pending) {
          reject(new Error(`Worker error: ${e.message}`));
        }
        this._pending.clear();
      };

      // Use a ping/pong handshake so we know the module is fully loaded.
      readyPromises.push(
        new Promise((resolve) => {
          const handler = (e) => {
            if (e.data?.type === 'ready') {
              worker.removeEventListener('message', handler);
              resolve();
            }
          };
          worker.addEventListener('message', handler);
          worker.postMessage({ type: 'ping' });
        }),
      );

      this._workers.push(worker);
    }

    // Workers that don't respond to ping within 2 s are still considered ready
    // (the mic-worker.js only processes 'decode-strip' messages, so the ping
    // just times out silently — that's fine for initialization).
    await Promise.race([
      Promise.all(readyPromises),
      new Promise((r) => setTimeout(r, 2000)),
    ]);

    this._ready = true;
  }

  /** @private */
  _onWorkerMessage(msg) {
    if (msg.type === 'plane-done') {
      const entry = this._pending.get(`plane-${msg.planeIndex}`);
      if (!entry) return;
      this._pending.delete(`plane-${msg.planeIndex}`);
      if (msg.error) {
        entry.reject(new Error(`plane ${msg.planeIndex}: ${msg.error}`));
      } else {
        entry.resolve(msg.planeBuf);
      }
      return;
    }

    if (msg.type !== 'strip-done') return;

    const entry = this._pending.get(msg.stripIndex);
    if (!entry) return;
    this._pending.delete(msg.stripIndex);

    if (msg.error) {
      entry.reject(new Error(`strip ${msg.stripIndex}: ${msg.error}`));
    } else {
      entry.resolve(msg.pixelBuffer ?? null); // null in SAB mode
    }
  }

  /**
   * Decode a PICS file using the worker pool.
   *
   * @param {Uint8Array} fileBytes  Complete PICS blob.
   * @returns {Promise<{ pixels: Uint16Array, width: number, height: number,
   *                     isPICS: true, numStrips: number, sabMode: boolean }>}
   */
  async decodePICS(fileBytes) {
    if (!this._ready) await this.init();

    const hdr = MICDecoder.parsePICSHeader(fileBytes);
    const { width, height, numStrips, stripH, strips, dataOffset } = hdr;

    // Allocate output buffer (shared or plain).
    let outBuffer;
    if (this._sabAvailable) {
      outBuffer = new SharedArrayBuffer(width * height * 2);
    } else {
      outBuffer = new ArrayBuffer(width * height * 2);
    }

    // Copy the entire PICS blob into a SharedArrayBuffer once (SAB mode) so
    // that every worker can read its strip without an extra copy.
    let fileSAB = null;
    if (this._sabAvailable) {
      fileSAB = new SharedArrayBuffer(fileBytes.byteLength);
      new Uint8Array(fileSAB).set(fileBytes);
    }

    // Collect per-strip pixel buffers (transferable mode only).
    const pixelChunks = new Array(numStrips).fill(null);

    // Dispatch all strips concurrently (round-robin across workers).
    const promises = strips.map((strip, s) => {
      const y0 = s * stripH;
      const y1 = Math.min(y0 + stripH, height);
      const sh = y1 - y0;
      const worker = this._workers[s % this._workers.length];

      return new Promise((resolve, reject) => {
        this._pending.set(s, {
          resolve: (pixelBuffer) => {
            pixelChunks[s] = pixelBuffer;
            resolve();
          },
          reject,
        });

        if (this._sabAvailable) {
          // SAB mode: zero-copy on both input and output.
          worker.postMessage({
            type: 'decode-strip',
            stripIndex: s,
            fileBuffer: fileSAB,
            fileOffset: dataOffset + strip.offset,
            fileLength: strip.length,
            outBuffer,
            outOffset: y0 * width,
            width,
            stripHeight: sh,
          });
        } else {
          // Transferable mode: copy just this strip's blob and transfer it.
          const blobCopy = fileBytes.slice(
            dataOffset + strip.offset,
            dataOffset + strip.offset + strip.length,
          ).buffer;
          worker.postMessage(
            {
              type: 'decode-strip',
              stripIndex: s,
              blobBuffer: blobCopy,
              width,
              stripHeight: sh,
            },
            [blobCopy],
          );
        }
      });
    });

    await Promise.all(promises);

    // In transferable mode, assemble pixel chunks into outBuffer.
    if (!this._sabAvailable) {
      const view = new Uint16Array(outBuffer);
      for (let s = 0; s < numStrips; s++) {
        const y0 = s * stripH;
        const chunk = new Uint16Array(pixelChunks[s]);
        view.set(chunk, y0 * width);
      }
    }

    return {
      pixels: new Uint16Array(outBuffer),
      width,
      height,
      isPICS: true,
      numStrips,
      sabMode: this._sabAvailable,
    };
  }

  /**
   * Decode a MICR single-frame RGB file by dispatching Y, Co, Cg planes to
   * three workers in parallel, then applying the YCoCg-R inverse on the main
   * thread once all planes are ready.
   *
   * Always uses transferable ArrayBuffers (the plane blobs are small enough
   * that SAB gives no meaningful advantage over a single copy per plane).
   *
   * @param {Uint8Array} fileBytes - Complete MICR file.
   * @returns {Promise<{ rgb: Uint8Array, width: number, height: number, isMICR: true }>}
   */
  async decodeRGBParallel(fileBytes) {
    if (!this._ready) await this.init();

    const { width, height, yBlob, coBlob, cgBlob } =
      MICDecoder.parseMICRPlanes(fileBytes);

    const planeBlobs = [yBlob, coBlob, cgBlob];
    const planeBufs  = new Array(3);

    const promises = planeBlobs.map((blob, i) => {
      const key = `plane-${i}`;
      return new Promise((resolve, reject) => {
        this._pending.set(key, {
          resolve: (buf) => { planeBufs[i] = buf; resolve(); },
          reject,
        });

        // Copy this plane's bytes into a fresh ArrayBuffer so we can transfer it.
        const planeBlobBuffer = blob.buffer.slice(
          blob.byteOffset,
          blob.byteOffset + blob.byteLength,
        );
        const worker = this._workers[i % this._workers.length];
        worker.postMessage(
          { type: 'decode-rgb-plane', planeIndex: i, width, height, planeBlobBuffer },
          [planeBlobBuffer],
        );
      });
    });

    await Promise.all(promises);

    const y  = new Uint16Array(planeBufs[0]);
    const co = new Uint16Array(planeBufs[1]);
    const cg = new Uint16Array(planeBufs[2]);
    const rgb = MICDecoder.applyYCoCgRInverse(y, co, cg, width, height);

    return { rgb, width, height, isMICR: true };
  }

  /**
   * Terminate all workers.  The decoder cannot be used after this call.
   */
  terminate() {
    for (const w of this._workers) w.terminate();
    this._workers = [];
    this._pending.clear();
    this._ready = false;
  }
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

/**
 * Create and initialise a PICSSABDecoder worker pool.
 * @param {number} [workerCount]
 * @returns {Promise<PICSSABDecoder>}
 */
export async function createPICSDecoder(workerCount) {
  const d = new PICSSABDecoder(workerCount);
  await d.init();
  return d;
}
