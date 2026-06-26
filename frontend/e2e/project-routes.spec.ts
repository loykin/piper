import { expect, test } from '@playwright/test'

const backend = 'http://127.0.0.1:18080'
const projectID = 'e2e'
const projectBase = `/api/projects/${projectID}`
const uiBase = `/ui/projects/${projectID}`

const routeYAML = `apiVersion: piper/v1
kind: Pipeline
metadata:
  name: frontend-route-check
spec:
  steps:
    - name: hello
      run:
        type: command
        command: ["sh", "-c", "echo route-check"]
`

test('project run and schedule detail routes are directly reachable', async ({ page }) => {
  const runResponse = await page.request.post(`${backend}${projectBase}/runs`, {
    data: { yaml: routeYAML },
  })
  expect(runResponse.ok()).toBeTruthy()
  const runBody = await runResponse.json() as { run_id: string }

  await page.goto(`${uiBase}/runs/${runBody.run_id}`)
  await expect(page).toHaveURL(new RegExp(`/ui/projects/${projectID}/runs/${runBody.run_id}$`))
  await expect(page.getByText(runBody.run_id)).toBeVisible()

  const scheduleResponse = await page.request.post(`${backend}${projectBase}/schedules`, {
    data: {
      name: 'frontend-route-check',
      yaml: routeYAML,
      type: 'cron',
      cron: '0 2 * * *',
    },
  })
  expect(scheduleResponse.ok()).toBeTruthy()
  const scheduleBody = await scheduleResponse.json() as { schedule_id: string }

  await page.goto(`${uiBase}/schedules/${scheduleBody.schedule_id}`)
  await expect(page).toHaveURL(new RegExp(`/ui/projects/${projectID}/schedules/${scheduleBody.schedule_id}$`))
  await expect(page.getByRole('heading', { name: 'frontend-route-check' })).toBeVisible()
})

test('unknown project route falls back to project schedules once', async ({ page }) => {
  await page.goto(`${uiBase}/missing/route`)
  await expect(page).toHaveURL(new RegExp(`/ui/projects/${projectID}/schedules$`))
  await expect(page.getByRole('heading', { name: 'Schedules' })).toBeVisible()
})
