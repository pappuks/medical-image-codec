// mic-pics-worker.mjs — Dedicated module Web Worker that runs the pthreaded
// C-PICS WASM decoder (web/vendor/mic-pics/mic-pics-decoder.mjs). The decode
// blocks on pthread_join, which is only permitted OFF the browser main thread —
// so the whole module lives here, in a worker, and its pthreads run as nested
// Web Workers spawned from this module's URL.
//
// Protocol:
//   in : { type:'init' }                       -> { type:'ready' } | { type:'error', error }
//   in : { type:'decode', id, bytes:Uint8Array } -> { type:'done', id, pixels:ArrayBuffer, width, height }
//                                                  | { type:'error', id, error }

import createMicPicsModule from '../vendor/mic-pics/mic-pics-decoder.mjs';

let Module = null;

async function ensureModule() {
  if (!Module) {
    Module = await createMicPicsModule({
      // ES6 build resolves the .wasm and its pthread workers relative to its own
      // import.meta.url; no locateFile override needed.
    });
  }
  return Module;
}

function readU32LE(b, off) {
  return (b[off] | (b[off + 1] << 8) | (b[off + 2] << 16) | (b[off + 3] << 24)) >>> 0;
}

function decode(bytes) {
  const M = Module;
  // PICS header: "PICS"(4) width(4) height(4) numStrips(4) ...
  if (String.fromCharCode(bytes[0], bytes[1], bytes[2], bytes[3]) !== 'PICS') {
    throw new Error('MIC-C-WASM-PICS: not a PICS blob');
  }
  const width = readU32LE(bytes, 4);
  const height = readU32LE(bytes, 8);

  const inPtr = M._malloc(bytes.length);
  const outPtr = M._malloc(width * height * 2);
  try {
    M.HEAPU8.set(bytes, inPtr);
    const rc = M._mic_pics_decode(inPtr, bytes.length, outPtr, width, height, 8);
    if (rc !== 0) throw new Error(`mic_pics_decode rc=${rc}`);
    // Fixed memory (no growth) → HEAPU16 stable. Copy pixels out to a plain
    // ArrayBuffer we can transfer back to the adapter.
    const pixels = M.HEAPU16.slice(outPtr >> 1, (outPtr >> 1) + width * height);
    return { pixels, width, height };
  } finally {
    M._free(inPtr);
    M._free(outPtr);
  }
}

self.onmessage = async (e) => {
  const msg = e.data;
  try {
    if (msg.type === 'init') {
      await ensureModule();
      self.postMessage({ type: 'ready' });
    } else if (msg.type === 'decode') {
      await ensureModule();
      const { pixels, width, height } = decode(msg.bytes);
      self.postMessage({ type: 'done', id: msg.id, pixels: pixels.buffer, width, height }, [pixels.buffer]);
    }
  } catch (err) {
    self.postMessage({ type: 'error', id: msg?.id, error: err.message });
  }
};
