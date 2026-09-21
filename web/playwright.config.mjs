// playwright.config.mjs — Headless-browser runner for the PACS dashboard.
// Auto-launches web/serve.py (which sets the COOP/COEP headers required for
// crossOriginIsolated / SharedArrayBuffer) around the test run.
import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  timeout: 180_000,
  reporter: [['list'], ['json', { outputFile: 'results/playwright-report.json' }]],
  use: {
    baseURL: 'http://localhost:8080',
    // COOP/COEP + SharedArrayBuffer work in headless Chromium given the headers.
    ...devices['Desktop Chrome'],
  },
  webServer: {
    command: 'python3 serve.py 8080',
    url: 'http://localhost:8080/pacs-dashboard.html',
    reuseExistingServer: !process.env.CI,
    timeout: 30_000,
  },
});
