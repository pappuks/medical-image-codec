// pacs-ai.spec.mjs — headless end-to-end test of the ?ai=1 dashboard mode:
// loads pacs-dashboard.html?ai=1&headless=1, waits for the AI run to finish,
// asserts the AI section rendered real numbers (decodeMs + inferMs).

import { test, expect } from '@playwright/test';

test('dashboard ?ai=1 runs the decode+inference pipeline', async ({ page }) => {
  test.setTimeout(240_000); // 31 MB model + ort runtime on first load

  await page.goto('/pacs-dashboard.html?ai=1&headless=1&images=quick');

  // headless mode flips this when the run completes (or errors)
  await page.waitForFunction(() => window.__pacsBenchDone === true, null, { timeout: 220_000 });

  const err = await page.evaluate(() => window.__pacsBenchError || null);
  if (err) throw new Error(`AI run failed: ${err}`);

  const result = await page.evaluate(() => window.__pacsBenchResult);
  expect(result).toBeTruthy();
  expect(result.aiRecords.length).toBeGreaterThan(0);

  const ok = result.aiRecords.filter((r) => r.pipelineMs != null);
  expect(ok.length).toBeGreaterThan(0);
  for (const r of ok) {
    expect(r.pipelineMs).toBeGreaterThanOrEqual(r.decodeMs);
    expect(r.inferMs).toBeGreaterThan(0);
  }

  // the AI panel must be visible with the summary line filled
  await expect(page.locator('#ai-panel')).toBeVisible();
  const summary = await page.locator('#ai-summary').textContent();
  expect(summary).toContain('inference backend');

  console.log(`AI dashboard OK: ${ok.length} images, e.g. ${ok[0].image} ` +
    `decode=${ok[0].decodeMs.toFixed(1)}ms infer=${ok[0].inferMs.toFixed(1)}ms`);
});