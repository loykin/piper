import { expect, test, type APIRequestContext, type Page } from '@playwright/test'

// Table-driven smoke over every resource page's destructive flow, clicked
// through the real UI: row → detail panel → action → ConfirmDialog. Each case
// checks the rules in docs/frontend/develop.md ("Resource UI Rules"):
// dialog copy derived from verb + noun, Cancel leaves the resource and the
// panel alone, confirming removes it and closes the panel only afterwards.
//
// Bugs this shape catches: a panel whose dialog click closed the panel first
// and dropped the action (side-panel outside-click vs Base UI dialogs), a
// dialog that closed before its mutation settled, a panel showing a snapshot.

const backend = 'http://127.0.0.1:18080'
const projectID = 'e2e'
const api = `${backend}/api/projects/${projectID}`
const uiBase = `/ui/projects/${projectID}`

async function ok(res: Awaited<ReturnType<APIRequestContext['get']>>) {
  expect(res.ok(), `${res.url()} → ${res.status()} ${await res.text()}`).toBeTruthy()
  return res
}

async function listNames(request: APIRequestContext, path: string, field: string): Promise<string[]> {
  const res = await ok(await request.get(`${api}${path}?limit=200`))
  return ((await res.json()) as Record<string, string>[]).map(item => item[field])
}

async function createWebhookCredential(request: APIRequestContext, name: string) {
  await ok(await request.post(`${api}/credentials`, {
    data: { name, kind: 'webhook', data: { url: 'https://hooks.example.com/piper' } },
  }))
}

interface Case {
  name: string
  /** Creates the resource; returns the text its list row shows. */
  create: (request: APIRequestContext, suffix: string) => Promise<string>
  listPath: string
  panelAction: string
  title: string
  confirmLabel: string
  /** Names still present on the server (by the displayed text). */
  remaining: (request: APIRequestContext) => Promise<string[]>
}

const cases: Case[] = [
  {
    name: 'credential',
    create: async (request, suffix) => {
      const name = `smoke-cred-${suffix}`
      await createWebhookCredential(request, name)
      return name
    },
    listPath: 'credentials',
    panelAction: 'Delete',
    title: 'Delete this credential?',
    confirmLabel: 'Delete Credential',
    remaining: request => listNames(request, '/credentials', 'name'),
  },
  {
    name: 'alert rule',
    create: async (request, suffix) => {
      const hook = `smoke-hook-${suffix}`
      await createWebhookCredential(request, hook)
      const name = `smoke-rule-${suffix}`
      await ok(await request.post(`${api}/alert-rules`, {
        data: { name, on: 'event', event_type: 'run.completed', notify: [hook], cooldown_seconds: 60 },
      }))
      return name
    },
    listPath: 'alert-rules',
    panelAction: 'Delete',
    title: 'Delete this alert rule?',
    confirmLabel: 'Delete Alert Rule',
    remaining: request => listNames(request, '/alert-rules', 'name'),
  },
  {
    name: 'schedule',
    create: async (request, suffix) => {
      const name = `smoke-schedule-${suffix}`
      await ok(await request.post(`${api}/schedules`, {
        data: {
          name,
          type: 'cron',
          cron: '0 3 * * *',
          yaml: `apiVersion: piper/v1\nkind: Pipeline\nmetadata:\n  name: ${name}\nspec:\n  steps:\n    - name: hi\n      run:\n        command: [echo, hi]\n`,
        },
      }))
      return name
    },
    listPath: 'schedules',
    panelAction: 'Delete',
    title: 'Delete this schedule?',
    confirmLabel: 'Delete Schedule',
    remaining: request => listNames(request, '/schedules', 'name'),
  },
  {
    name: 'storage object',
    create: async (request, suffix) => {
      const key = `smoke-object-${suffix}.txt`
      await ok(await request.post(`${api}/storage/objects`, {
        multipart: { key, file: { name: key, mimeType: 'text/plain', buffer: Buffer.from('smoke') } },
      }))
      return key
    },
    listPath: 'storage',
    panelAction: 'Delete',
    title: 'Delete this object?',
    confirmLabel: 'Delete Object',
    remaining: request => listNames(request, '/storage/objects', 'key'),
  },
]

async function openPanel(page: Page, listPath: string, rowText: string) {
  await page.goto(`${uiBase}/${listPath}`)
  await page.getByText(rowText, { exact: true }).first().click()
  const panel = page.locator('.side-panel-panel')
  await expect(panel).toBeVisible()
  await expect(panel.getByText(rowText).first()).toBeVisible()
  return panel
}

for (const c of cases) {
  test(`${c.name}: delete from the detail panel follows the confirm contract`, async ({ page, request }) => {
    const rowText = await c.create(request, `${Date.now()}`)
    const panel = await openPanel(page, c.listPath, rowText)
    const dialog = page.getByRole('alertdialog')

    // Cancel: nothing happens, the panel stays.
    await panel.getByRole('button', { name: c.panelAction, exact: true }).click()
    await expect(dialog).toBeVisible()
    await expect(dialog.getByText(c.title)).toBeVisible()
    await expect(dialog.getByText(rowText)).toBeVisible()
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
    await expect(dialog).toBeHidden()
    await expect(panel).toBeVisible()
    expect(await c.remaining(request)).toContain(rowText)

    // Confirm: the resource is gone and only then does the panel close.
    await panel.getByRole('button', { name: c.panelAction, exact: true }).click()
    await dialog.getByRole('button', { name: c.confirmLabel, exact: true }).click()
    await expect(dialog).toBeHidden()
    await expect(panel).toBeHidden()
    await expect.poll(() => c.remaining(request)).not.toContain(rowText)
    await expect(page.getByText(rowText, { exact: true })).toHaveCount(0)
  })
}

// The panel reads the rule by id, so it reflects each toggle and computes the
// next one from the current value — a row snapshot sent `enabled: false`
// twice and the rule could never be re-enabled from the panel.
test('alert rule panel toggles enable/disable repeatedly and stays current', async ({ page, request }) => {
  const suffix = `${Date.now()}`
  const rowText = await cases[1].create(request, suffix)
  const panel = await openPanel(page, 'alert-rules', rowText)
  const enabledOnServer = async () => {
    const res = await ok(await request.get(`${api}/alert-rules?limit=200`))
    return ((await res.json()) as { name: string; enabled: boolean }[]).find(r => r.name === rowText)?.enabled
  }

  await panel.getByRole('button', { name: 'Disable', exact: true }).click()
  await expect(panel.getByText('Disabled', { exact: true })).toBeVisible()
  await expect.poll(enabledOnServer).toBe(false)

  await panel.getByRole('button', { name: 'Enable', exact: true }).click()
  await expect(panel.getByText('Enabled', { exact: true })).toBeVisible()
  await expect.poll(enabledOnServer).toBe(true)
})

// The schedule name used to default to "my-pipeline" and was regex-written
// over the first `name:` line, ignoring the YAML's own metadata.name.
test('a new schedule takes its name from the YAML unless one is typed', async ({ page, request }) => {
  const yamlName = `sched-yaml-${Date.now()}`
  await page.goto(`${uiBase}/schedules/new`)
  await expect(page.locator('#schedule-pipeline-name')).toHaveValue('')
  await page.getByRole('button', { name: /^Cron/ }).click()
  const editor = page.locator('.cm-content')
  await editor.click()
  await page.keyboard.press('ControlOrMeta+A')
  await page.keyboard.insertText(`apiVersion: piper/v1
kind: Pipeline
metadata:
  name: ${yamlName}
spec:
  steps:
    - name: hello
      run:
        command: [echo, hi]
`)
  await expect(page.locator('#schedule-pipeline-name')).toHaveAttribute('placeholder', yamlName)
  await page.getByRole('button', { name: 'Create Schedule', exact: true }).click()
  await expect(page.getByRole('heading', { name: yamlName })).toBeVisible()

  const typedName = `sched-typed-${Date.now()}`
  await page.goto(`${uiBase}/schedules/new`)
  await page.locator('#schedule-pipeline-name').fill(typedName)
  await page.getByRole('button', { name: /^Cron/ }).click()
  await page.getByRole('button', { name: 'Create Schedule', exact: true }).click()
  await expect(page.getByRole('heading', { name: typedName })).toBeVisible()

  const list = await request.get(`${backend}/api/projects/${projectID}/schedules?limit=100&offset=0`)
  const schedules = await list.json() as Array<{ name: string; pipeline_yaml: string }>
  const typed = schedules.find(s => s.name === typedName)
  expect(typed?.pipeline_yaml).toContain(`name: ${typedName}`)
  expect(typed?.pipeline_yaml).toContain('name: hello') // step names untouched
})
