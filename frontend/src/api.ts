// piper server API client

export interface Run {
  id: string
  schedule_id?: string
  owner_id?: string
  pipeline_name: string
  status: 'scheduled' | 'running' | 'success' | 'failed' | 'canceled'
  started_at: string
  ended_at?: string
  scheduled_at?: string
  pipeline_yaml: string
  steps?: Step[]
}

export interface Schedule {
  id: string
  name: string
  owner_id?: string
  pipeline_yaml: string
  schedule_type: 'immediate' | 'once' | 'cron'
  cron_expr?: string
  enabled: boolean
  last_run_at?: string
  next_run_at: string
  created_at: string
  updated_at: string
}

export interface Step {
  run_id: string
  step_name: string
  status: 'pending' | 'running' | 'done' | 'failed' | 'skipped' | 'canceled'
  started_at?: string
  ended_at?: string
  attempts: number
  error?: string
}

export interface LogLine {
  id: number
  run_id: string
  step_name: string
  ts: string
  stream: 'stdout' | 'stderr'
  line: string
}

export interface CreateRunOptions {
  params?: Record<string, unknown>
  owner_id?: string
  vars?: {
    scheduled_at?: string
  }
}

export interface CreateScheduleOptions {
  name: string
  yaml: string
  type: 'immediate' | 'once' | 'cron'
  cron?: string
  run_at?: string  // ISO string for type=once
  owner_id?: string
  params?: Record<string, unknown>
}

const BASE = ''

export async function listRuns(filter?: { status?: string; pipeline?: string }): Promise<Run[]> {
  const params = new URLSearchParams()
  if (filter?.status) params.set('status', filter.status)
  if (filter?.pipeline) params.set('pipeline_name', filter.pipeline)
  const qs = params.toString()
  const res = await fetch(`${BASE}/runs${qs ? `?${qs}` : ''}`)
  if (!res.ok) throw new Error(`listRuns: ${res.status}`)
  const data: unknown = await res.json()
  return Array.isArray(data) ? (data as Run[]) : []
}

export async function getRun(id: string): Promise<{ run: Run; steps: Step[] }> {
  const res = await fetch(`${BASE}/runs/${id}`)
  if (!res.ok) throw new Error(`getRun: ${res.status}`)
  return res.json()
}

export async function getLogs(runID: string, stepName: string, afterID = 0): Promise<LogLine[]> {
  const res = await fetch(`${BASE}/runs/${runID}/steps/${stepName}/logs?after=${afterID}`)
  if (!res.ok) throw new Error(`getLogs: ${res.status}`)
  return res.json()
}

export function streamLogs(
  runID: string,
  stepName: string,
  onLine: (line: LogLine) => void,
  onDone: (status: string) => void,
): EventSource {
  const es = new EventSource(`${BASE}/runs/${runID}/steps/${stepName}/logs/stream`)
  es.onmessage = (e) => {
    try { onLine(JSON.parse(e.data)) } catch { /* skip */ }
  }
  es.addEventListener('done', (e: MessageEvent) => {
    try { onDone(JSON.parse(e.data).status) } catch { onDone('unknown') }
    es.close()
  })
  return es
}

export async function createRun(yaml: string, options?: CreateRunOptions): Promise<{ run_id: string }> {
  const res = await fetch(`${BASE}/runs`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      yaml,
      params: options?.params,
      owner_id: options?.owner_id,
      vars: options?.vars,
    }),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
  return res.json()
}

export async function listSchedules(): Promise<Schedule[]> {
  const res = await fetch(`${BASE}/schedules`)
  if (!res.ok) throw new Error(`listSchedules: ${res.status}`)
  const data: unknown = await res.json()
  return Array.isArray(data) ? (data as Schedule[]) : []
}

export async function getSchedule(id: string): Promise<Schedule> {
  const res = await fetch(`${BASE}/schedules/${id}`)
  if (!res.ok) throw new Error(`getSchedule: ${res.status}`)
  return res.json()
}

export async function listScheduleRuns(scheduleId: string): Promise<Run[]> {
  const res = await fetch(`${BASE}/schedules/${scheduleId}/runs`)
  if (!res.ok) throw new Error(`listScheduleRuns: ${res.status}`)
  const data: unknown = await res.json()
  return Array.isArray(data) ? (data as Run[]) : []
}

export async function createSchedule(options: CreateScheduleOptions): Promise<{ schedule_id: string }> {
  const res = await fetch(`${BASE}/schedules`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(options),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
  return res.json()
}

export async function setScheduleEnabled(id: string, enabled: boolean): Promise<void> {
  const res = await fetch(`${BASE}/schedules/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ enabled }),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
}

export async function deleteSchedule(id: string): Promise<void> {
  const res = await fetch(`${BASE}/schedules/${id}`, { method: 'DELETE' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
}

// ── Services ──────────────────────────────────────────────────────────────────

export interface Service {
  name: string
  run_id: string
  artifact: string   // "step/artifact_name"
  status: 'running' | 'stopped' | 'failed'
  endpoint: string   // e.g. "http://localhost:9001"
  namespace?: string
  pid: number
  yaml: string
  created_at: string
  updated_at: string
}

export async function listServing(): Promise<Service[]> {
  const res = await fetch(`${BASE}/serving`)
  if (!res.ok) throw new Error(`listServing: ${res.status}`)
  const data: unknown = await res.json()
  return Array.isArray(data) ? (data as Service[]) : []
}

export async function getServing(name: string): Promise<Service> {
  const res = await fetch(`${BASE}/serving/${name}`)
  if (!res.ok) throw new Error(`getServing: ${res.status}`)
  return res.json()
}

export async function deployServing(yaml: string): Promise<{ name: string }> {
  const res = await fetch(`${BASE}/serving`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ yaml }),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
  return res.json()
}

export async function stopServing(name: string): Promise<void> {
  const res = await fetch(`${BASE}/serving/${name}`, { method: 'DELETE' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
}

export async function restartServing(name: string): Promise<void> {
  const res = await fetch(`${BASE}/serving/${name}/restart`, { method: 'POST' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
}

// ─── Artifacts ───────────────────────────────────────────────────────────────

export interface ArtifactFile {
  path: string
  size: number
  modified_at: string
}

export interface ArtifactEntry {
  name: string
  files: ArtifactFile[]
}

export interface StepArtifacts {
  step: string
  artifacts: ArtifactEntry[]
}

export async function listArtifacts(runID: string): Promise<StepArtifacts[]> {
  const res = await fetch(`${BASE}/runs/${runID}/artifacts`)
  if (!res.ok) throw new Error(`listArtifacts: ${res.status}`)
  const data: unknown = await res.json()
  return Array.isArray(data) ? (data as StepArtifacts[]) : []
}

/** Returns the URL to download a specific artifact file (use as href or window.open). */
export function artifactDownloadURL(runID: string, step: string, artifact: string, filePath: string): string {
  return `${BASE}/runs/${runID}/artifacts/${step}/${artifact}/${filePath}`
}

export async function deleteRun(id: string): Promise<void> {
  const res = await fetch(`${BASE}/runs/${id}`, { method: 'DELETE' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
}

export async function cancelRun(id: string): Promise<void> {
  const res = await fetch(`${BASE}/runs/${id}/cancel`, { method: 'POST' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
}

export async function rerunRun(id: string, failedOnly = false): Promise<{ run_id: string }> {
  const res = await fetch(`${BASE}/runs/${id}/rerun`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ failed_only: failedOnly }),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
  return res.json()
}

export async function retryStep(runID: string, stepName: string): Promise<{ run_id: string }> {
  const res = await fetch(`${BASE}/runs/${runID}/steps/${encodeURIComponent(stepName)}/retry`, { method: 'POST' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
  return res.json()
}

export interface Worker {
  id: string
  label: string
  version: string
  capabilities: string
  hostname: string
  concurrency: number
  status: string
  in_flight: number
  registered_at: string
  last_seen: string
}

export async function listWorkers(): Promise<Worker[]> {
  const res = await fetch(`${BASE}/api/workers`)
  if (!res.ok) throw new Error(`listWorkers: ${res.status}`)
  return res.json()
}

export interface ServiceHistory {
  id: number
  name: string
  run_id: string
  artifact: string
  status: string
  endpoint: string
  namespace?: string
  pid: number
  yaml: string
  deployed_at: string
  stopped_at: string
}

export async function listServingHistory(): Promise<ServiceHistory[]> {
  const res = await fetch(`${BASE}/serving/history`)
  if (!res.ok) throw new Error(`listServingHistory: ${res.status}`)
  const data: unknown = await res.json()
  return Array.isArray(data) ? (data as ServiceHistory[]) : []
}
