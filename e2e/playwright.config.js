// Dev-only browser gate. Playwright is never part of the server build or image;
// CI installs it separately and points it at a real Wren TV deployment.
import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  // Only the gate spec: the web unit tests in this repository are node:test
  // files and are not Playwright tests.
  testMatch: '*.spec.js',
  timeout: 60_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  // Every spec drives the same deployment and the same fixture provider, whose
  // request counters and rate-limit switch are what half the assertions read.
  // One worker keeps those observations attributable to one test at a time.
  workers: 1,
  retries: 0,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: process.env.WRENTV_BASE_URL || 'http://localhost:8099',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
