// runs feature API
export type {
  Run, Step, LogLine, CreateRunOptions,
  ArtifactFile, ArtifactEntry, StepArtifacts, RunFilter,
  SweepRequest, SweepResponse, RunMetrics,
} from './types'

import type { Run, Step, LogLine, CreateRunOptions, StepArtifacts, RunFilter, SweepRequest, SweepResponse, RunMetrics } from './types'

const BASE = ''

export async function listRuns(filter?: RunFilter): Promise<Run[]> {
  const params = new URLSearchParams()
  if (filter?.status) params.set('status', filter.status)
  if (filter?.pipeline) params.set('pipeline_name', filter.pipeline)
  if (filter?.experiment) params.set('experiment', filter.experiment)
  if (filter?.metric_step) params.set('metric_step', filter.metric_step)
  if (filter?.metric_key) params.set('metric_key', filter.metric_key)
  if (filter?.metric_order) params.set('metric_order', filter.metric_order)
  const qs = params.toString()
  const res = await fetch(`${BASE}/runs${qs ? `?${qs}` : ''}`)
  if (!res.ok) throw new Error(`listRuns: ${res.status}`)
  const data: unknown = await res.json()
  return Array.isArray(data) ? (data as Run[]) : []
}

export async function createSweep(req: SweepRequest): Promise<SweepResponse> {
  const res = await fetch(`${BASE}/runs/sweep`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
  return res.json()
}

export async function getRunMetrics(runID: string): Promise<RunMetrics> {
  const res = await fetch(`${BASE}/runs/${runID}/metrics`)
  if (!res.ok) return {}
  const raw: unknown = await res.json()
  if (!Array.isArray(raw)) return {}
  const out: RunMetrics = {}
  for (const m of raw as Array<{ step_name: string; key: string; value: number }>) {
    if (!out[m.step_name]) out[m.step_name] = {}
    out[m.step_name][m.key] = m.value
  }
  return out
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
