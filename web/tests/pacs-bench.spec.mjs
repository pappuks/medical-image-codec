// pacs-bench.spec.mjs — Drives the PACS dashboard in a real headless browser,
// validating that (a) every live codec (MIC 1/4/8-state, PICS, HTJ2K, JPEG-LS)
// actually decodes in-browser, (b) decoded pixels are bit-exact against the
// manifest checksums, and (c) results are machine-readable. Writes the full
// result JSON to results/ for regression tracking.
//
// Run:  cd web && npx playwright test
// (Requires: `go run ./cmd/mic-compress -testdata` and, for reference codecs,
//  `go run -tags cgo_ojph ./cmd/mic-refgen`, plus `bash scripts/vendor-wasm.sh`.)
import { test, expect } from '@playwright/test';
import { writeFileSync, mkdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const __dir = dirname(fileURLToPath(import.meta.url));

test('PACS dashboard decodes all live codecs and verifies pixel-correctness', async ({ page }) => {
  // Track only genuine JS errors. Benign resource 404s (favicon, and codec
  // variants that legitimately don't exist for some images, e.g. a 4-strip
  // PICS file for an image only shipped with 8 strips) are expected and
  // handled gracefully by the runner, so they're filtered out here.
  const jsErrors = [];
  const isBenignResource = (t) =>
    /Failed to load resource/.test(t) || /favicon/.test(t) ||
    /ERR_EMPTY_RESPONSE/.test(t) || /the server responded with a status of 404/.test(t);
  page.on('console', (m) => {
    if (m.type() === 'error' && !isBenignResource(m.text())) jsErrors.push(m.text());
  });
  page.on('pageerror', (e) => jsErrors.push('pageerror: ' + e.message));

  await page.goto('/pacs-dashboard.html?headless=1&images=quick&iterations=5&warmup=2&verify=1&allprofiles=1');

  await page.waitForFunction(() => window.__pacsBenchDone === true, null, { timeout: 150_000 });

  const runError = await page.evaluate(() => window.__pacsBenchError);
  expect(runError, `dashboard reported error: ${runError}`).toBeFalsy();

  const result = await page.evaluate(() => window.__pacsBenchResult);
  expect(result, 'no result object').toBeTruthy();
  expect(result.records.length, 'no measurements produced').toBeGreaterThan(0);

  // crossOriginIsolated should be true under COOP/COEP; PICS relies on it for
  // zero-copy SAB. (Warn-only: transferable fallback still works.)
  if (result.env.crossOriginIsolated !== true) {
    console.warn('crossOriginIsolated is not true — PICS running in transferable fallback mode');
  }

  // Every live codec that produced a compressed file must pixel-verify.
  const live = result.records.filter((r) => r.liveDecode && r.compressedBytes != null);
  expect(live.length, 'no live-decoded records').toBeGreaterThan(0);
  for (const r of live) {
    expect(r.pixelsVerified, `${r.image}/${r.codecId} pixel mismatch (${r.note ?? ''})`).toBe(true);
  }

  // The vendored WASM reference codecs and the Go/WASM MIC build must all be
  // present & live (they exercise three distinct WASM loading paths).
  const liveIds = new Set(live.map((r) => r.codecId));
  expect(liveIds.has('htj2k'), 'HTJ2K did not decode live').toBeTruthy();
  expect(liveIds.has('jpegls'), 'JPEG-LS did not decode live').toBeTruthy();
  expect(liveIds.has('mic-wasm'), 'MIC-WASM (Go) did not decode live').toBeTruthy();
  expect(liveIds.has('mic-c-wasm-4'), 'MIC-C-WASM (4-state) did not decode live').toBeTruthy();
  expect(liveIds.has('mic-c-wasm-8'), 'MIC-C-WASM (8-state) did not decode live').toBeTruthy();
  // The pthreaded C-PICS WASM (runs off-main-thread, fans out to pthread
  // workers). Only images with an 8-strip PICS file produce a record (CR in the
  // quick set), so assert it decoded live for at least one image.
  expect(liveIds.has('pics-c-wasm-8'), 'MIC-C-WASM-PICS did not decode live').toBeTruthy();

  // No genuine JS/page errors during the run (benign resource 404s filtered).
  expect(jsErrors, `js errors:\n${jsErrors.join('\n')}`).toEqual([]);

  const outDir = resolve(__dir, '../results'); // web/results/ (gitignored)
  mkdirSync(outDir, { recursive: true });
  const stamp = new Date().toISOString().replace(/[:.]/g, '-');
  const outPath = resolve(outDir, `pacs-bench-${stamp}.json`);
  writeFileSync(outPath, JSON.stringify(result, null, 2));
  console.log(`Wrote ${outPath} (${result.records.length} records)`);
});
