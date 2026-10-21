import { expect, test } from '@playwright/test'

const api = '/api/projects/e2e'
const ui = '/ui/projects/e2e'
const image = 'jupyter/minimal-notebook:qa-regression'

// Deliberately preload server data before clicking the primary actions. These
// bugs only surfaced when query results and separate mount effects overlapped.
test.beforeEach(async ({ page }) => {
  await page.route('**/api/system/settings', async route => {
    const response = await route.fetch()
    const settings = await response.json()
    await route.fulfill({ json: { ...settings, runtime: { type: 'k8s', namespaces: ['qa-runtime'] } } })
  })
})

test('source setup waits for a notebook choice before filling environment defaults (OE-1)', async ({ page }) => {
  await page.route(`**${api}/notebooks`, route => route.fulfill({ json: [{
    name: 'source-notebook', volume_id: 'frontend-e2e-volume', image,
    status: 'running', created_at: '2026-10-21T00:00:00Z', updated_at: '2026-10-21T00:00:00Z',
  }] }))
  await page.goto(`${ui}/pipelines`)
  await page.getByRole('button', { name: 'New Template', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Start Editing', exact: true })).toBeVisible()
  // Let both queries resolve while the source setup is still mounted.
  await expect(page.locator('#pipeline-volume')).toContainText('Frontend E2E Workspace')
  await page.getByRole('button', { name: 'Start Editing', exact: true }).click()
  await expect(page.getByText('Container Image', { exact: true }).locator('..').getByRole('textbox')).toHaveValue(image)
  await expect(page.getByText('Namespace', { exact: true }).locator('..').getByRole('textbox')).toHaveValue('qa-runtime')
})

test('cached runtime keeps sole pipeline source selection on repeated deployment (KQA-2)', async ({ page }) => {
  await page.route(`**${api}/runs?**`, route => route.fulfill({ json: [{
    id: 'qa-run', pipeline_name: 'qa-model', status: 'success', started_at: '2026-10-21T00:00:00Z',
  }] }))
  await page.route(`**${api}/runs/qa-run/artifacts`, route => route.fulfill({ json: [{
    step: 'train', artifacts: [{ name: 'model', files: [{ path: 'model.json', size: 10 }] }],
  }] }))
  await page.goto(`${ui}/serving`)
  for (let attempt = 0; attempt < 2; attempt++) {
    await page.getByRole('button', { name: 'Deploy Service', exact: true }).click()
    await expect(page.getByRole('textbox', { name: 'Pipeline', exact: true })).toHaveValue('qa-model')
    await expect(page.getByRole('combobox', { name: 'Run', exact: true })).toBeEnabled()
    await expect(page.getByRole('textbox', { name: 'Step', exact: true })).toHaveValue('train')
    // A sole artifact also auto-selects; assert the source survives another
    // field update and appears in the serialized manifest, not just the label.
    await expect(page.locator('#deploy-artifact')).toHaveValue('model')
    await page.getByRole('textbox', { name: 'Service Name', exact: true }).fill(`qa-service-${attempt}`)
    await page.getByRole('tab', { name: 'YAML', exact: true }).click()
    await expect(page.locator('.cm-content')).toContainText('qa-model')
    await page.getByRole('button', { name: 'Cancel', exact: true }).click()
  }
})


test('deployment defaults to the server allowed namespace (KQA-1)', async ({ page }) => {
  await page.goto(`${ui}/serving`)
  await page.getByRole('button', { name: 'Deploy Service', exact: true }).click()
  await expect(page.getByRole('textbox', { name: 'Namespace', exact: true })).toHaveValue('qa-runtime')
})
