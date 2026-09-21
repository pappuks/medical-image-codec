// probe-onnx.mjs — Node smoke test for the browser AI model.
// Loads web/models/brain-segmentation-unet.onnx via onnxruntime-web's WASM
// backend (Node has no WebGPU) and asserts a [1,3,256,256] forward pass
// returns [1,1,256,256]. Run: cd web && node scripts/probe-onnx.mjs

import ort from 'onnxruntime-web';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const here = path.dirname(fileURLToPath(import.meta.url));
const modelPath = path.join(here, '..', 'models', 'brain-segmentation-unet.onnx');

// Node: feed the model bytes directly (ort-web resolves relative paths
// against its own worker scope, which breaks file loading here).
const modelBytes = new Uint8Array(readFileSync(modelPath));

console.log('ort-web version:', ort.env.versions.web);
console.log(`model: ${(modelBytes.length / 1e6).toFixed(1)} MB`);

const session = await ort.InferenceSession.create(modelBytes, {
  executionProviders: ['wasm'],
});

console.log('input names :', session.inputNames);
console.log('output names:', session.outputNames);

const H = 256, W = 256;
const data = new Float32Array(1 * 3 * H * W).fill(0.5);
const feeds = { input: new ort.Tensor('float32', data, [1, 3, H, W]) };

// warmup + median of 5
const times = [];
let out;
for (let i = 0; i < 6; i++) {
  const t0 = performance.now();
  const res = await session.run(feeds);
  times.push(performance.now() - t0);
  if (i === 0) out = res;
}
times.shift(); // drop warmup
times.sort((a, b) => a - b);
const median = times[Math.floor(times.length / 2)];

const dims = out['output'].dims;
console.log('output dims :', dims);
console.log(`median inference (wasm, 5 runs): ${median.toFixed(1)} ms`);

if (dims.length !== 4 || dims[0] !== 1 || dims[1] !== 1 || dims[2] !== H || dims[3] !== W) {
  console.error(`FAIL: expected [1,1,${H},${W}], got`, dims);
  process.exit(1);
}
console.log('PASS: brain U-Net forward pass returns [1,1,256,256]');