// onnx-adapter.mjs — browser inference adapter for the PACS AI benchmark
// section. Wraps onnxruntime-web behind an init/infer shape that mirrors the
// codec-adapter contract (codec-interface.md), so the runner's timing
// discipline applies to inference the same way it does to decodes.
//
// Model: mateuszbuda/brain-segmentation-pytorch U-Net (MIT), exported to
// ONNX — see pacs-ai-model.md. NOT for clinical use.
//
// Contract:
//   init()                — create the InferenceSession (WebGPU, WASM fallback)
//   infer(pixels, w, h)   — grayscale uint16 slice -> Float32Array [1,1,256,256]
//   dispose()             — release the session
//   backend               — which execution provider actually ran ('webgpu'|'wasm')

// ort-web must be lazy-loaded (dynamic import) so the plain decode dashboard
// never pays for the ~10 MB runtime when ?ai=1 is absent.
// Browser: the vendored, fully self-contained ESM bundle (no bare imports —
// this repo serves raw ES modules with no bundler). Node (probe/CI): the npm
// package resolves normally.
let ortPromise = null;
function loadOrt() {
  if (!ortPromise) {
    const isBrowser = typeof window !== 'undefined' && typeof document !== 'undefined';
    ortPromise = import(
      isBrowser ? '../vendor/onnx/ort.all.bundle.min.mjs' : 'onnxruntime-web'
    );
  }
  return ortPromise;
}

export const MODEL_URL = 'models/brain-segmentation-unet.onnx';
export const MODEL_INPUT = 'input';
export const MODEL_OUTPUT = 'output';
export const MODEL_SIZE = 256; // the U-Net's fixed spatial size

export function makeOnnxAdapter({ modelUrl = MODEL_URL } = {}) {
  let ort = null;
  let session = null;
  let backend = null;

  const adapter = {
    id: 'onnx-unet',
    label: 'AI: brain U-Net (ONNX)',
    // inference is a live browser measurement, same as live decodes
    liveDecodeSupported: true,

    async init() {
      ort = await loadOrt();
      // WebGPU first (Chrome/Edge 113+; FP16 needs 121+), WASM everywhere.
      // In Node (probe/CI) only 'wasm' exists; ort skips unknown providers
      // with a warning, so listing both is safe in the browser and Node.
      try {
        session = await ort.InferenceSession.create(modelUrl, {
          executionProviders: ['webgpu', 'wasm'],
        });
        backend = session.inputNames ? 'webgpu-or-wasm' : 'wasm';
      } catch (e) {
        session = await ort.InferenceSession.create(modelUrl, {
          executionProviders: ['wasm'],
        });
        backend = 'wasm';
      }
      // Ask ort which provider it actually picked (available 1.17+).
      try {
        const ep = session?.handler?.graphOptimizer?.constructor?.name;
        if (!ep) backend = (typeof navigator !== 'undefined' && navigator.gpu) ? 'webgpu?' : 'wasm';
      } catch { /* keep fallback label */ }
      adapter.backend = backend;
    },

    // pixels: Uint16Array (grayscale, window/level-applied or raw), w x h.
    // Returns { mask: Float32Array(256*256), ms } — probability map [0,1].
    async infer(pixels, w, h) {
      if (!session) throw new Error('onnx adapter not initialised');
      const input = preprocessToModelInput(pixels, w, h);
      const feeds = { [MODEL_INPUT]: new ort.Tensor('float32', input.data, [1, 3, MODEL_SIZE, MODEL_SIZE]) };
      const t0 = performance.now();
      const res = await session.run(feeds);
      const ms = performance.now() - t0;
      return { mask: res[MODEL_OUTPUT].data, ms };
    },

    dispose() {
      if (session) {
        try { session.release(); } catch { /* already released */ }
        session = null;
      }
    },
  };

  return adapter;
}

// Grayscale uint16 [0..65535] -> 3x MODEL_SIZExMODEL_SIZE float32 in [0,1].
// Simple min-max normalisation over the slice (matches the model's 3-channel
// FLAIR input shape; per-window normalisation is a future refinement).
export function preprocessToModelInput(pixels, w, h) {
  const S = MODEL_SIZE;
  const data = new Float32Array(3 * S * S);
  if (!pixels || !pixels.length) return { data, w: S, h: S };

  // min-max normalise the source slice
  let min = Infinity, max = -Infinity;
  for (let i = 0; i < pixels.length; i++) {
    const v = pixels[i];
    if (v < min) min = v;
    if (v > max) max = v;
  }
  const range = max > min ? max - min : 1;

  // box-sample the source to S x S (nearest-neighbour is enough for the demo;
  // keep it allocation-light: one pass, direct channel writes)
  for (let y = 0; y < S; y++) {
    const sy = Math.min(h - 1, (y * h / S) | 0);
    const rowOff = sy * w;
    for (let x = 0; x < S; x++) {
      const sx = Math.min(w - 1, (x * w / S) | 0);
      const v = (pixels[rowOff + sx] - min) / range;
      const o = y * S + x;
      data[o] = v;             // R
      data[S * S + o] = v;     // G
      data[2 * S * S + o] = v; // B
    }
  }
  return { data, w: S, h: S };
}