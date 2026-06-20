import { defineConfig } from '@playwright/test'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const frontendDir = path.dirname(fileURLToPath(import.meta.url))
const repoRoot = path.resolve(frontendDir, '..')
const pythonEnv = process.env.PIPER_PIPELINE_TEMPLATE_E2E_ENV ?? '/tmp/piper-pipeline-template-e2e'
const executablePath = process.env.PLAYWRIGHT_EXECUTABLE_PATH

export default defineConfig({
  testDir: './e2e',
  timeout: 120_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  workers: 1,
  use: {
    baseURL: 'http://127.0.0.1:4173',
    headless: true,
    browserName: 'chromium',
    launchOptions: executablePath ? { executablePath } : undefined,
    trace: 'retain-on-failure',
  },
  webServer: [
    {
      command: `PATH=${pythonEnv}/bin:$PATH go run ./examples/frontend-e2e --addr=127.0.0.1:18080`,
      cwd: repoRoot,
      url: 'http://127.0.0.1:18080/e2e/ready',
      timeout: 120_000,
      reuseExistingServer: false,
    },
    {
      command: 'PIPER_BACKEND_URL=http://127.0.0.1:18080 pnpm dev --host 127.0.0.1 --port 4173',
      cwd: frontendDir,
      url: 'http://127.0.0.1:4173/ui/pipelines',
      timeout: 120_000,
      reuseExistingServer: false,
    },
  ],
})
