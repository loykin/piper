import { expect, test, type Page } from '@playwright/test'

// Auth-mode screens the trusted-mode suite can't reach: first-run bootstrap,
// Users, and Project Members. Runs against a fresh database (see
// playwright.auth.config.ts), so the test creates its own admin account.

const admin = { username: 'e2e-admin', password: 'e2e-admin-password-1' }
const projectPath = '/ui/projects/default'

async function signIn(page: Page) {
  await page.goto('/ui/login')
  const heading = page.getByRole('heading', { name: /Create the admin account|Sign in to piper/ })
  await expect(heading).toBeVisible()
  const bootstrap = (await heading.textContent())?.includes('Create the admin account')
  await page.locator('#username').fill(admin.username)
  await page.locator('#password').fill(admin.password)
  await page.getByRole('button', { name: bootstrap ? 'Create Admin Account' : 'Sign In', exact: true }).click()
  await expect(page).not.toHaveURL(/\/login/)
}

async function createUser(page: Page, username: string) {
  await page.goto('/ui/users')
  await page.getByRole('button', { name: 'New User', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'New User', exact: true })).toBeVisible()
  await page.locator('#create-user-username').fill(username)
  await page.locator('#create-user-password').fill('temporary-password-1')
  await page.getByRole('button', { name: 'Create User', exact: true }).click()
  await expect(page.getByText(username, { exact: true })).toBeVisible()
}

test('bootstrap, then manage users and project members through the UI', async ({ page }) => {
  await signIn(page)
  const suffix = Date.now()
  const member = `e2e-member-${suffix}`
  const temp = `e2e-temp-${suffix}`
  await createUser(page, member)
  await createUser(page, temp)

  // Users: the panel reads the user by id and the confirm flow deletes it.
  await page.getByText(temp, { exact: true }).click()
  const panel = page.locator('.side-panel-panel')
  await expect(panel.getByText(temp).first()).toBeVisible()
  await panel.getByRole('button', { name: `Delete ${temp}` }).click()
  const dialog = page.getByRole('alertdialog')
  await expect(dialog.getByText('Delete this user?')).toBeVisible()
  await dialog.getByRole('button', { name: 'Delete User', exact: true }).click()
  await expect(dialog).toBeHidden()
  await expect(panel).toBeHidden()
  await expect(page.getByText(temp, { exact: true })).toHaveCount(0)

  // Members: add through the API, then change the role from the panel.
  const added = await page.request.post(`/api/projects/default/members`, { data: { username: member, role: 'viewer' } })
  expect(added.ok(), await added.text()).toBeTruthy()
  await page.goto(`${projectPath}/members`)
  await page.getByText(member, { exact: true }).click()
  await expect(panel.getByText(member).first()).toBeVisible()

  // NEW-16: the role Select renders in a portal outside the panel; choosing
  // an option used to count as an outside click and close the panel. The
  // panel must stay open and show the new role (it reads the member by id).
  await panel.getByRole('combobox').click()
  await page.getByRole('option', { name: 'Admin' }).click()
  await expect(panel).toBeVisible()
  await expect(panel.getByRole('combobox')).toContainText('Admin')
  await expect.poll(async () => {
    const res = await page.request.get(`/api/projects/default/members`)
    const members = (await res.json()) as { username: string; role: string }[]
    return members.find(m => m.username === member)?.role
  }).toBe('admin')

  // Remove through the confirm contract.
  await panel.getByRole('button', { name: `Remove ${member}` }).click()
  await expect(dialog.getByText('Remove this member?')).toBeVisible()
  await dialog.getByRole('button', { name: 'Remove Member', exact: true }).click()
  await expect(dialog).toBeHidden()
  await expect(panel).toBeHidden()
  await expect(page.getByText(member, { exact: true })).toHaveCount(0)
})
