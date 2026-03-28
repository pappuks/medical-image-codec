// bench-worker.mjs — Node.js worker_threads equivalent of mic-worker.js.
// Used exclusively by bench-decoder.mjs for parallel PICS benchmarking.
// Not for browser use; see mic-worker.js for the browser version.

import { parentPort } from 'node:worker_threads';
import { MICDecoder } from './mic-decoder.js';

parentPort.on('message', (msg) => {
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

    const pixels = MICDecoder.decode(blob, width, stripHeight);

    if (msg.outBuffer) {
      // SAB mode: write pixels directly into the shared output buffer
      const outView = new Uint16Array(msg.outBuffer, msg.outOffset * 2, width * stripHeight);
      outView.set(pixels);
      parentPort.postMessage({ type: 'strip-done', stripIndex, error: null });
    } else {
      // Transferable mode: transfer pixel buffer back
      const pixelBuffer = pixels.buffer.slice(0);
      parentPort.postMessage(
        { type: 'strip-done', stripIndex, pixelBuffer, error: null },
        [pixelBuffer],
      );
    }
  } catch (err) {
    parentPort.postMessage({ type: 'strip-done', stripIndex, error: err.message });
  }
});
