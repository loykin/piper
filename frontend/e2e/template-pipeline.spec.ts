import { expect, test } from '@playwright/test'

const backend = 'http://127.0.0.1:18080'
const projectID = 'e2e'
const projectBase = `/api/projects/${projectID}`
const uiBase = `/ui/projects/${projectID}`

const pipelineYAML = `apiVersion: piper/v1
kind: Pipeline
metadata:
  name: frontend-mixed
spec:
  steps:
    - name: prepare-data
      run:
        type: command
        command:
          - sh
          - -c
          - 'printf "{\\"multiplier\\":3,\\"values\\":[1,2,3,4]}\\\\n" > "$PIPER_OUTPUT_DIR/seed.json"'
      outputs:
        - name: seed
          path: seed.json
    - name: build-features
      depends_on: [prepare-data]
      run:
        type: notebook
        notebook: notebooks/build_features.ipynb
        deps:
          - support
      inputs:
        - name: seed
          from: prepare-data/seed
      outputs:
        - name: features
          path: features.json
    - name: summarize
      depends_on: [build-features]
      run:
        type: python
        path: scripts/summarize.py
        deps:
          - support
        command:
          - sh
          - -c
          - 'PYTHONPATH="$PWD" python3 "$PIPER_SCRIPT_PATH"'
      inputs:
        - name: features
          from: build-features/features
      outputs:
        - name: summary
          path: summary.json
`

test('submits and runs a mixed template with dependency files in S3', async ({ page }) => {
  await page.goto(`${uiBase}/pipelines/editor?source=notebook-volume&volume=frontend-e2e-volume&name=frontend-mixed`)
  await expect(page.getByRole('heading', { name: 'Pipeline Editor' })).toBeVisible()
  await expect(page.getByText('Frontend E2E Workspace')).toBeVisible()

  await page.getByRole('tab', { name: 'YAML' }).click()
  const editor = page.locator('.cm-content')
  await editor.click()
  await page.keyboard.press('ControlOrMeta+A')
  await page.keyboard.insertText(pipelineYAML)
  await page.getByRole('button', { name: 'Apply YAML to Graph' }).click()

  await page.getByRole('tab', { name: 'YAML' }).click()
  await expect(page.locator('.cm-content')).toContainText('notebooks/build_features.ipynb')
  await expect(page.locator('.cm-content')).toContainText('scripts/summarize.py')
  await expect(page.locator('.cm-content')).toContainText('support')

  await page.getByRole('button', { name: 'Submit' }).click()
  await expect(page.getByRole('heading', { name: 'Submit Pipeline Template' })).toBeVisible()
  await page.getByRole('button', { name: 'Confirm Submit' }).click()
  await page.waitForURL(new RegExp(`/ui/projects/${projectID}/pipelines\\?name=frontend-mixed`))

  const templatesResponse = await page.request.get(
    `${backend}${projectBase}/pipelines?name=frontend-mixed`,
  )
  expect(templatesResponse.ok()).toBeTruthy()
  const templates = await templatesResponse.json() as Array<{ id: string; snapshot_id: string }>
  expect(templates).toHaveLength(1)

  const snapshotResponse = await page.request.get(
    `${backend}/e2e/objects?prefix=${encodeURIComponent(`snapshots/${templates[0].snapshot_id}/`)}`,
  )
  const snapshotKeys = await snapshotResponse.json() as string[]
  expect(snapshotKeys).toEqual(expect.arrayContaining([
    `snapshots/${templates[0].snapshot_id}/notebooks/build_features.ipynb`,
    `snapshots/${templates[0].snapshot_id}/scripts/summarize.py`,
    `snapshots/${templates[0].snapshot_id}/support/math_utils.py`,
    `snapshots/${templates[0].snapshot_id}/support/summary_utils.py`,
  ]))

  await page.getByRole('button', { name: 'Run' }).click()
  await page.waitForURL(new RegExp(`/ui/projects/${projectID}/runs/run-`))
  const runID = page.url().split('/').pop()!

  await expect.poll(async () => {
    const response = await page.request.get(`${backend}${projectBase}/runs/${runID}`)
    const body = await response.json() as { run: { status: string } }
    return body.run.status
  }, { timeout: 90_000 }).toBe('success')

  const summaryResponse = await page.request.get(
    `${backend}${projectBase}/runs/${runID}/artifacts/summarize/summary/summary.json`,
  )
  expect(summaryResponse.ok()).toBeTruthy()
  expect(await summaryResponse.json()).toEqual({
    count: 4,
    sum: 30,
    source: 'frontend-template',
  })
})
