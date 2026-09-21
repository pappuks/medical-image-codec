// onnx-adapter.spec.mjs — Playwright smoke test for the AI adapter.
// Exercises the full pipeline against the local dev server: load a real
// decode from the codec adapters, preprocess, run inference, check output.

import { test, expect } from '@playwright/test';

test.describe('ONNX AI adapter', () => {
  // timeout comes from playwright.config.mjs (180s; the model is 31 MB).
  test('init + infer on a real MIC decode', async ({ page }) => {
    await page.goto('/index.html');
    await page.waitForFunction(() => true); // settle

    const result = await page.evaluate(async () => {
      const { makeOnnxAdapter, preprocessToModelInput, MODEL_SIZE } =
        await import('./codecs/onnx-adapter.mjs');
      const { MICDecoder } = await import('./mic-decoder.js');

      // real decode: the CT test image
      const blob = new Uint8Array(
        await (await fetch('testdata/CT.mic')).arrayBuffer()
      );
      const { pixels, width, height } = MICDecoder.decodeFile(blob);

      const adapter = makeOnnxAdapter();
      await adapter.init();

      // inference on the real decoded pixels
      const t0 = performance.now();
      const { mask, ms } = await adapter.infer(pixels, width, height);
      const wallMs = performance.now() - t0;

      // preprocess invariants on a second call path
      const pp = preprocessToModelInput(pixels, width, height);

      adapter.dispose();

      return {
        size: MODEL_SIZE,
        backend: adapter.backend,
        maskLen: mask.length,
        ms, wallMs,
        ppLen: pp.data.length,
        inRange: mask.every((v) => v >= 0 && v <= 1.0001),
        someSignal: mask.some((v) => v > 0.01),
        minMaxNorm: pp.data.every((v) => v >= 0 && v <= 1),
      };
    });

    expect(result.maskLen).toBe(result.size * result.size);
    expect(result.ppLen).toBe(3 * result.size * result.size);
    expect(result.inRange).toBe(true);       // sigmoid output in [0,1]
    expect(result.someSignal).toBe(true);    // non-degenerate output
    expect(result.minMaxNorm).toBe(true);    // preprocessor normalised
    expect(result.ms).toBeGreaterThan(0);
    console.log(`AI adapter OK: backend=${result.backend} infer=${result.ms.toFixed(1)}ms wall=${result.wallMs.toFixed(1)}ms`);
  });
});