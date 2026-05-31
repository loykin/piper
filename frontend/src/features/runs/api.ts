// runs feature API

const BASE = ''

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
