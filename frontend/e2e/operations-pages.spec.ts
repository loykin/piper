import { expect, test } from '@playwright/test'

const projectID = 'e2e'
const uiBase = `/ui/projects/${projectID}`
const backend = 'http://127.0.0.1:18080'

test('notebook executions page exposes empty state and persisted policy', async ({ page }) => {
  await page.goto(`${uiBase}/notebook-executions`)
  await expect(page.getByRole('heading', { name: 'Notebook Executions' })).toBeVisible()
  await expect(page.getByText('No notebook executions yet.')).toBeVisible()

  const policy = page.getByText('Execution policy').locator('..').locator('[role="combobox"]')
  await policy.click()
  await page.getByRole('option', { name: 'Allowed' }).click()
  await expect(policy).toContainText('Allowed')
  await page.reload()
  await expect(page.getByText('Execution policy').locator('..').locator('[role="combobox"]')).toContainText('Allowed')
})

test('creates an MLflow credential and manages an integration through the UI', async ({ page }) => {
  await page.goto(`${uiBase}/credentials/new`)
  await page.locator('#credential-name').fill('qa-mlflow-credential')
  await page.locator('#credential-kind').click()
  await page.getByRole('option', { name: 'MLflow' }).click()
  await page.getByPlaceholder('secret value').fill('qa-token')
  await page.getByRole('button', { name: 'Create Credential' }).click()
  await page.waitForURL(new RegExp(`${uiBase}/credentials$`))
  await expect(page.getByText('qa-mlflow-credential')).toBeVisible()

  await page.goto(`${uiBase}/integrations/mlflow`)
  await expect(page.getByRole('heading', { name: 'MLflow Integrations' })).toBeVisible()
  await expect(page.getByText('No MLflow integrations yet.')).toBeVisible()
  await page.getByRole('button', { name: 'New MLflow Integration' }).click()
  await page.locator('#mlflow-name').fill('qa-mlflow')
  await page.locator('#mlflow-uri').fill('https://mlflow.example.com')
  // Exactly one MLflow credential exists at this point, so the form
  // auto-selects it into a disabled display input instead of an
  // interactive combobox (see the `soleCredential` handling in
  // MLflowIntegrationForm.tsx) — assert the auto-fill rather than clicking.
  await expect(page.locator('#mlflow-credential')).toHaveValue('qa-mlflow-credential')
  await page.getByRole('button', { name: 'Create MLflow Integration' }).click()
  await page.waitForURL(new RegExp(`${uiBase}/integrations/mlflow$`))

  const row = page.getByRole('row', { name: /qa-mlflow/ })
  await expect(row).toContainText('disabled')
  await row.click()
  await expect(page.getByText(/MLflow dispatch is disabled in the server configuration/)).toBeVisible()
  await page.getByRole('button', { name: 'Edit' }).click()
  await expect(page.locator('#mlflow-name')).toHaveValue('qa-mlflow')
  await page.getByRole('button', { name: 'Cancel' }).click()

  await row.click()
  await page.getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByRole('alertdialog')).toContainText('existing MLflow runs are not deleted.')
  const deleteResponse = page.waitForResponse(response => response.request().method() === 'DELETE' && response.url().includes('/mlflow-integrations/'))
  // A real click: this used to need dispatchEvent because the click that
  // reached the dialog also closed the side panel underneath it first.
  await page.getByRole('alertdialog').getByRole('button', { name: 'Delete Integration' }).click()
  expect((await deleteResponse).status()).toBe(204)
  const listResponse = await page.request.get(`${backend}/api/projects/${projectID}/mlflow-integrations?limit=20&offset=0`)
  expect(await listResponse.json()).toEqual([])
  await expect(row).toHaveCount(0)
  await expect(page.getByText('No MLflow integrations yet.')).toBeVisible()
})
