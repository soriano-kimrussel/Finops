import { defineConfig, devices } from '@playwright/test'

// Screenshot capture against an already running dev stack (see screenshots/capture.spec.ts).
export default defineConfig({
  testDir: './screenshots',
  workers: 1,
  reporter: 'list',
  use: { baseURL: 'http://127.0.0.1:5173' },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1 } }],
})
