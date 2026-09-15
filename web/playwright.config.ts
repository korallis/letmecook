import { defineConfig, devices } from '@playwright/test';

// Runs against the real embedded daemon started per test file (see tests/daemon.ts);
// no Vite dev server, no proxy, no cross-origin exception.
export default defineConfig({
  testDir: 'tests',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['list'], ['json', { outputFile: 'reports/playwright.json' }]],
  outputDir: 'reports/test-results',
  use: { ...devices['Desktop Chrome'], trace: 'off', video: 'off' },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
