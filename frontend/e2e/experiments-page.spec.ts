import { expect, test } from '@playwright/test'

const backend = 'http://127.0.0.1:18080'
const projectID = 'e2e'
const projectBase = `/api/projects/${projectID}`
const uiBase = `/ui/projects/${projectID}`

test('creates a sweep through the rendered form and lists its server aggregate', async ({ page }) => {
  const suffix = Date.now()
  const pipelineName = `sweep-pipeline-${suffix}`
  const experimentName = `sweep-${suffix}`
  const yaml = `apiVersion: piper/v1
kind: Pipeline
metadata:
  name: ${pipelineName}
spec:
  steps:
    - name: trial
      run:
        type: command
        command: [sh, -c, "echo trial"]
`
  const created = await page.request.post(`${backend}${projectBase}/pipelines`, { data: { yaml } })
  expect(created.ok()).toBeTruthy()

  await page.goto(`${uiBase}/experiments`)
  await page.getByRole('button', { name: 'New Sweep' }).click()
  await expect(page).toHaveURL(new RegExp(`${uiBase}/experiments/new$`))
  await page.locator('#experiment-name').fill(experimentName)
  // The project has exactly one pipeline at this point, so the form
  // auto-selects it into a disabled display input instead of an
  // interactive combobox (see the `solePipeline` handling in
  // ExperimentCreatePage.tsx) — assert the auto-fill rather than clicking.
  await expect(page.locator('#sweep-pipeline')).toHaveValue(`${pipelineName} v1`)
  await page.locator('#sweep-trials').fill('[{"rate":0.1},{"rate":0.2}]')
  await page.getByRole('button', { name: 'Create Sweep' }).click()

  await expect(page).toHaveURL(new RegExp(`${uiBase}/experiments$`))
  await expect(page.getByText(experimentName, { exact: true })).toBeVisible()
  const response = await page.request.get(`${backend}${projectBase}/experiments?name=${experimentName}&limit=1&offset=0`)
  expect(response.ok()).toBeTruthy()
  expect(response.headers()['x-total-count']).toBe('1')
  const rows = await response.json() as Array<{ name: string; runs: number; success: number; failed: number; running: number; latest: string }>
  expect(rows).toHaveLength(1)
  expect(rows[0].name).toBe(experimentName)
  expect(rows[0].runs).toBe(2)
  expect(rows[0].success + rows[0].failed + rows[0].running).toBeLessThanOrEqual(2)
  expect(rows[0].latest).not.toBe('')
})
