// piper server API client

export interface Run {
  id: string
  owner_id?: string
  pipeline_name: string
  status: 'running' | 'success' | 'failed'
  started_at: string
  finished_at?: string
  pipeline_yaml: string
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

const BASE = ''

export async function listRuns(): Promise<Run[]> {
  const res = await fetch(`${BASE}/runs`)
  if (!res.ok) throw new Error(`listRuns: ${res.status}`)
  return res.json()
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

export async function createRun(yaml: string, params?: Record<string, unknown>): Promise<{ run_id: string }> {
  const res = await fetch(`${BASE}/runs`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ yaml, params }),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
  return res.json()
}
