import { expect, test } from '@playwright/test'

const backend = 'http://127.0.0.1:18080'
const projectID = 'e2e'
const uiBase = `/ui/projects/${projectID}`

// V — the "Add S3-compatible Credential" form on the Storage page's
// Configuration tab used to send no request at all and show no error when
// submitted with an empty Name, because the submit button was simply
// disabled with no explanation. This mirrors AlertRuleCreatePage's
// 'Name is required.' pattern: the button stays enabled once the
// kind-specific fields are filled, and an empty-name submit surfaces a
// visible error instead of doing nothing.
test('storage credential form shows an explicit error on empty-name submit, and succeeds once named', async ({ page }) => {
  await page.goto(`${uiBase}/storage?tab=config`)
  await expect(page.getByRole('heading', { name: 'Storage' })).toBeVisible()

  await page.getByRole('button', { name: 'New credential' }).click()

  await page.getByRole('textbox', { name: 'access_key_id' }).fill('AKIAEXAMPLE')
  await page.getByLabel('secret_access_key').fill('supersecretvalue')

  const addButton = page.getByRole('button', { name: /Add s3 Credential/ })
  await addButton.click()

  // No silent no-op: an explicit, visible validation error.
  await expect(page.getByText('Name is required.')).toBeVisible()

  // Filling the name clears the error and a real submit now succeeds —
  // confirms the fix only adds validation, it doesn't break the happy path.
  const credentialName = `e2e-cred-${Date.now()}`
  const nameInput = page.getByPlaceholder('s3-artifacts')
  await nameInput.fill(credentialName)
  await expect(page.getByText('Name is required.')).not.toBeVisible()
  await addButton.click()

  await expect(page.getByText(credentialName, { exact: true })).toBeVisible()

  // Clean up so this credential doesn't leak into other specs sharing the
  // same backend process. The credential name renders as a direct sibling
  // of its own "Delete" button inside one row div.
  const row = page.getByText(credentialName, { exact: true }).locator('..')
  await row.getByRole('button', { name: 'Delete' }).click()
  await page.getByRole('button', { name: 'Delete credential' }).click()
  await expect(page.getByText(credentialName, { exact: true })).not.toBeVisible()
})

// W — the Objects tab used to show its loading skeleton forever with no
// error banner when the storage backend was unreachable, because the
// underlying query could get stuck in TanStack Query's retry-pause state
// (parked waiting for the tab to regain focus/come online) without ever
// reaching status 'error'. /e2e/storage/break flips the fake S3 backend
// this harness runs to return 500 for every request, reproducing a real
// "GET .../storage/objects returns 500" backend outage end-to-end.
test('storage objects tab shows an explicit error instead of hanging when the backend is unreachable', async ({ page }) => {
  const breakResp = await page.request.post(`${backend}/e2e/storage/break`)
  expect(breakResp.ok()).toBeTruthy()

  try {
    await page.goto(`${uiBase}/storage?tab=objects`)
    await expect(page.getByRole('heading', { name: 'Storage' })).toBeVisible()

    await expect(page.getByText(/Failed to load uploaded objects/)).toBeVisible()
    await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible()
  } finally {
    const fixResp = await page.request.post(`${backend}/e2e/storage/fix`)
    expect(fixResp.ok()).toBeTruthy()
  }

  // Retry against the now-healthy backend clears the error.
  await page.getByRole('button', { name: 'Retry' }).click()
  await expect(page.getByText(/Failed to load uploaded objects/)).not.toBeVisible()
})
