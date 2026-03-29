// mic-worker.js — ES module Web Worker for parallel MIC decoding.
//
// Handles two message types:
//
//   decode-strip — decode one PICS strip (greyscale)
//   decode-rgb-plane — decode one Y/Co/Cg plane of a MICR RGB image
//
// ─── decode-strip ────────────────────────────────────────────────────────────
// Each worker handles one strip at a time.  Two modes are supported:
//
//   SharedArrayBuffer mode (requires crossOriginIsolated):
//     - fileBuffer: SharedArrayBuffer  — the whole PICS file, zero-copy
//     - outBuffer:  SharedArrayBuffer  — output pixel array (uint16), zero-copy
//     - Worker reads its slice from fileBuffer, decodes, writes to outBuffer.
//     - Posts {type:'strip-done', stripIndex, error} (no pixel data transferred).
//
//   Transferable mode (fallback when SAB is unavailable):
//     - blobBuffer: ArrayBuffer — transferred (zero-copy) input blob for one strip
//     - Worker decodes, posts back {type:'strip-done', stripIndex, pixelBuffer, error}
//       where pixelBuffer is a transferred ArrayBuffer containing the decoded uint16 pixels.
//
// Message in:
//   {
//     type:        'decode-strip',
//     stripIndex:  number,
//     // SAB mode:
//     fileBuffer:  SharedArrayBuffer,
//     fileOffset:  number,    // byte offset of this strip's blob in fileBuffer
//     fileLength:  number,    // byte length of this strip's blob
//     outBuffer:   SharedArrayBuffer,
//     outOffset:   number,    // uint16 element index in outBuffer where this strip starts
//     // Transferable mode:
//     blobBuffer:  ArrayBuffer,
//     // Both modes:
//     width:       number,
//     stripHeight: number,
//   }
//
// Message out (SAB mode):
//   { type: 'strip-done', stripIndex: number, error: string|null }
//
// Message out (transferable mode):
//   { type: 'strip-done', stripIndex: number, pixelBuffer: ArrayBuffer, error: string|null }

import { MICDecoder } from './mic-decoder.js';

self.onmessage = function (e) {
  const msg = e.data;

  // ─── RGB plane decode (for parallel MICR channel decoding) ───────────────
  if (msg.type === 'decode-rgb-plane') {
    const { planeIndex, width, height } = msg;
    try {
      const blob = new Uint8Array(msg.planeBlobBuffer);
      const plane = MICDecoder.decodeRGBPlane(blob, width, height);
      // Copy into a new ArrayBuffer before transferring (plane.buffer may be
      // larger than the plane if the FSE output was over-allocated).
      const planeBuf = plane.buffer.slice(plane.byteOffset, plane.byteOffset + plane.byteLength);
      self.postMessage(
        { type: 'plane-done', planeIndex, planeBuf, error: null },
        [planeBuf],
      );
    } catch (err) {
      self.postMessage({ type: 'plane-done', planeIndex, planeBuf: null, error: err.message });
    }
    return;
  }

  if (msg.type !== 'decode-strip') return;

  const { stripIndex, width, stripHeight } = msg;

  try {
    let blob;

    if (msg.fileBuffer) {
      // SharedArrayBuffer mode: read strip slice directly (no copy)
      blob = new Uint8Array(msg.fileBuffer, msg.fileOffset, msg.fileLength);
    } else {
      // Transferable mode: entire blobBuffer is this strip
      blob = new Uint8Array(msg.blobBuffer);
    }

    // Decode: FSE + RLE + Delta pipeline (auto-detects 2-state vs 4-state)
    const pixels = MICDecoder.decode(blob, width, stripHeight);

    if (msg.outBuffer) {
      // SAB mode: write pixels directly into the shared output buffer
      const outView = new Uint16Array(msg.outBuffer, msg.outOffset * 2, width * stripHeight);
      outView.set(pixels);
      self.postMessage({ type: 'strip-done', stripIndex, error: null });
    } else {
      // Transferable mode: transfer pixel buffer back to main thread
      const pixelBuffer = pixels.buffer.slice(0);
      self.postMessage(
        { type: 'strip-done', stripIndex, pixelBuffer, error: null },
        [pixelBuffer],
      );
    }
  } catch (err) {
    self.postMessage({ type: 'strip-done', stripIndex, error: err.message });
  }
};
