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
  await expect(page.getByText('No MLflow integrations configured.')).toBeVisible()
  await page.getByRole('button', { name: 'New Integration' }).click()
  await page.locator('#mlflow-name').fill('qa-mlflow')
  await page.locator('#mlflow-uri').fill('https://mlflow.example.com')
  await page.locator('#mlflow-credential').click()
  await page.getByRole('option', { name: 'qa-mlflow-credential' }).click()
  await page.getByRole('button', { name: 'Save Integration' }).click()
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
  await expect(page.getByRole('alertdialog')).toContainText('Existing MLflow runs are not deleted.')
  const deleteResponse = page.waitForResponse(response => response.request().method() === 'DELETE' && response.url().includes('/mlflow-integrations/'))
  // Base UI's modal focus/inert layer can swallow Playwright's synthetic
  // pointer/keyboard input in a hidden headless tab (see the QA playbook).
  // Dispatch against the rendered action after separately verifying that
  // the real confirmation dialog and button are visible.
  const deleteAction = page.getByRole('alertdialog').getByRole('button', { name: 'Delete' })
  await expect(deleteAction).toBeVisible()
  await deleteAction.dispatchEvent('click')
  expect((await deleteResponse).status()).toBe(204)
  const listResponse = await page.request.get(`${backend}/api/projects/${projectID}/mlflow-integrations?limit=20&offset=0`)
  expect(await listResponse.json()).toEqual([])
  await expect(row).toHaveCount(0)
  await expect(page.getByText('No MLflow integrations configured.')).toBeVisible()
})
