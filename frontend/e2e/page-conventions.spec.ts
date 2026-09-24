import { expect, test } from '@playwright/test'

// Table-driven checks of the page-level rules in docs/frontend/develop.md
// ("Resource UI Rules"), clicked through the real UI.

const backend = 'http://127.0.0.1:18080'
const projectID = 'e2e'
const uiBase = `/ui/projects/${projectID}`

// The list's create button, the create page's title and last breadcrumb, and
// the submit button all come from createCopy(noun, verb) — they used to name
// the same flow four different ways ("Deploy Service" / "New Service" /
// "Deploy" / "Deploy").
const createFlows = [
  { list: 'credentials', action: 'New Credential', submit: 'Create Credential' },
  { list: 'alert-rules', action: 'New Alert Rule', submit: 'Create Alert Rule' },
  { list: 'schedules', action: 'New Schedule', submit: 'Create Schedule' },
  { list: 'experiments', action: 'New Sweep', submit: 'Create Sweep' },
  { list: 'serving', action: 'Deploy Service', submit: 'Deploy Service' },
  { list: 'notebooks', action: 'Launch Notebook', submit: 'Launch Notebook' },
  { list: 'integrations/mlflow', action: 'New MLflow Integration', submit: 'Create MLflow Integration' },
]

for (const flow of createFlows) {
  test(`create flow "${flow.action}" is named the same everywhere`, async ({ page }) => {
    await page.goto(`${uiBase}/${flow.list}`)
    await page.getByRole('button', { name: flow.action, exact: true }).click()
    await expect(page.getByRole('heading', { name: flow.action, exact: true })).toBeVisible()
    await expect(page.locator('[data-slot="breadcrumb-page"]')).toHaveText(flow.action)
    await expect(page.getByRole('button', { name: flow.submit, exact: true })).toBeVisible()
  })
}

// Only a 404 reads as "not found"; the page keeps its breadcrumbs so the
// user can go back. (A 500 used to say "doesn't exist or may have been
// deleted" too.)
const missing = [
  { path: 'runs/no-such-run', title: 'Run Not Found', back: 'Run History' },
  { path: 'schedules/no-such-schedule', title: 'Schedule Not Found', back: 'Schedules' },
  { path: 'credentials/no-such-credential/rotate', title: 'Credential Not Found', back: 'Credentials' },
]

for (const m of missing) {
  test(`missing resource at ${m.path} shows "${m.title}" with a way back`, async ({ page }) => {
    await page.goto(`${uiBase}/${m.path}`)
    await expect(page.getByRole('heading', { name: m.title })).toBeVisible()
    await expect(page.getByRole('link', { name: m.back })).toBeVisible()
  })
}

// FormActions' own status line is muted text; a failed submit rendered there
// looked like a neutral note. FormSubmitBar shows it in the destructive color.
test('a failed form submit shows its error in the destructive color', async ({ page, request }) => {
  const name = `dup-cred-${Date.now()}`
  const created = await request.post(`${backend}/api/projects/${projectID}/credentials`, {
    data: { name, kind: 'generic', data: { k: 'v' } },
  })
  expect(created.ok()).toBeTruthy()

  await page.goto(`${uiBase}/credentials/new`)
  await page.locator('#credential-name').fill(name)
  await page.getByPlaceholder('api_key').first().fill('k')
  await page.getByRole('button', { name: 'Create Credential', exact: true }).click()

  const status = page.getByRole('status').locator('.text-destructive')
  await expect(status).toBeVisible()
  await expect(status).not.toBeEmpty()
})
