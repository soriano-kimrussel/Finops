import { defineConfig, devices } from '@playwright/test'

// End-to-end tests run against a real Go API (port 8081, fresh finops_e2e database, simulated
// QuickBooks) and the Vite dev server (port 5174). Both are started and stopped automatically.
export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: 'http://127.0.0.1:5174',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } } }],
  webServer: [
    {
      command: 'powershell -NoProfile -ExecutionPolicy Bypass -File ../scripts/e2e-server.ps1',
      url: 'http://127.0.0.1:8081/readyz',
      timeout: 180_000,
      reuseExistingServer: false,
      stdout: 'pipe',
    },
    {
      command: 'npx vite --host 127.0.0.1 --port 5174 --strictPort',
      url: 'http://127.0.0.1:5174',
      timeout: 60_000,
      reuseExistingServer: false,
      env: { VITE_API_TARGET: 'http://127.0.0.1:8081', VITE_PORT: '5174' },
    },
  ],
})
