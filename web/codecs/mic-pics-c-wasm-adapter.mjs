// mic-pics-c-wasm-adapter.mjs — Adapter for the pthreaded C-PICS WASM decoder.
// The actual decode runs inside mic-pics-worker.mjs (off the main thread, so
// pthread_join may block); this adapter owns that worker and marshals bytes in
// / pixels out. It's the C-scheduler analogue of the pure-JS PICS Web Worker
// pool — one worker here fans out to N pthread workers internally.

const WORKER_URL = new URL('./mic-pics-worker.mjs', import.meta.url);

export function makeMICPicsCWasmAdapter({ id = 'pics-c-wasm-8', label = 'MIC-C-WASM-PICS (8 strips)' } = {}) {
  let worker = null;
  let seq = 0;
  const pending = new Map();

  return {
    id,
    label,
    liveDecodeSupported: true,
    async init() {
      worker = new Worker(WORKER_URL, { type: 'module' });
      worker.onmessage = (e) => {
        const m = e.data;
        if (m.type === 'ready') { pending.get('init')?.resolve(); pending.delete('init'); return; }
        const p = pending.get(m.id);
        if (!p) return;
        pending.delete(m.id);
        if (m.type === 'error') p.reject(new Error(m.error));
        else p.resolve(m);
      };
      worker.onerror = (e) => {
        const err = new Error(`mic-pics worker error: ${e.message}`);
        for (const p of pending.values()) p.reject(err);
        pending.clear();
      };
      await new Promise((resolve, reject) => {
        pending.set('init', { resolve, reject });
        worker.postMessage({ type: 'init' });
      });
    },
    async decode(bytes) {
      const id = ++seq;
      const { pixels, width, height } = await new Promise((resolve, reject) => {
        pending.set(id, { resolve, reject });
        // Structured-clone the input (no transfer) so the runner's reused
        // `bytes` buffer stays intact across warmup/timed/verify iterations.
        worker.postMessage({ type: 'decode', id, bytes });
      });
      return { pixels: new Uint16Array(pixels), width, height, bitsPerSample: 16, signed: false };
    },
    dispose() {
      if (worker) worker.terminate();
      worker = null;
    },
  };
}
