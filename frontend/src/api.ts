// piper server API client

export interface Run {
  id: string
  owner_id?: string
  pipeline_name: string
  status: 'scheduled' | 'running' | 'success' | 'failed'
  started_at: string
  finished_at?: string
  scheduled_at?: string
  pipeline_yaml: string
}

export interface Schedule {
  id: string
  name: string
  owner_id?: string
  pipeline_yaml: string
  schedule_type: 'cron' | 'once'
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
  status: 'pending' | 'running' | 'done' | 'failed' | 'skipped'
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
  cron?: string
  run_at?: string  // ISO string for once-type
  owner_id?: string
  params?: Record<string, unknown>
}

const BASE = ''

export async function listRuns(): Promise<Run[]> {
  const res = await fetch(`${BASE}/runs`)
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
