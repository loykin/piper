import { defineConfig } from '@playwright/test'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

// Auth-mode e2e: `piper server` with built-in authentication on a fresh
// database (the main suite runs a trusted-mode server). The spec creates the
// admin account itself through the bootstrap screen.
const frontendDir = path.dirname(fileURLToPath(import.meta.url))
const executablePath = process.env.PLAYWRIGHT_EXECUTABLE_PATH

export default defineConfig({
  testDir: './e2e-auth',
  timeout: 120_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  workers: 1,
  use: {
    baseURL: 'http://127.0.0.1:4174',
    headless: true,
    browserName: 'chromium',
    launchOptions: executablePath ? { executablePath } : undefined,
    trace: 'retain-on-failure',
  },
  webServer: [
    {
      command: './e2e-auth/start-server.sh',
      cwd: frontendDir,
      url: 'http://127.0.0.1:18081/health',
      timeout: 180_000,
      reuseExistingServer: false,
    },
    {
      command: 'PIPER_BACKEND_URL=http://127.0.0.1:18081 pnpm build && PIPER_BACKEND_URL=http://127.0.0.1:18081 pnpm preview --host 127.0.0.1 --port 4174',
      cwd: frontendDir,
      url: 'http://127.0.0.1:4174/ui/',
      timeout: 180_000,
      reuseExistingServer: false,
    },
  ],
})
