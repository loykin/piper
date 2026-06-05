// pipelines feature API — pipeline template management

const BASE = '/api'

export interface PipelineTemplate {
  id: string
  name: string
  yaml: string
  snapshot_id: string
  volume_id: string
  created_at: string
}

export interface SubmitPipelineRequest {
  name: string
  yaml: string
  volume_id?: string
}

export interface TriggerRunRequest {
  params?: Record<string, unknown>
}

export interface DeployRequest {
  cron: string
  enabled?: boolean
  params?: Record<string, unknown>
}

async function throwOnError(res: Response): Promise<void> {
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error((err as { error?: string }).error ?? res.statusText)
  }
}

export async function listPipelines(name?: string, limit?: number): Promise<PipelineTemplate[]> {
  const params = new URLSearchParams()
  if (name) params.set('name', name)
  if (limit) params.set('limit', String(limit))
  const url = `${BASE}/pipelines${params.size > 0 ? '?' + params.toString() : ''}`
  const res = await fetch(url)
  if (!res.ok) throw new Error(`listPipelines: ${res.status}`)
  const data: unknown = await res.json()
  return Array.isArray(data) ? (data as PipelineTemplate[]) : []
}

export async function submitPipeline(req: SubmitPipelineRequest): Promise<PipelineTemplate> {
  const res = await fetch(`${BASE}/pipelines`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  await throwOnError(res)
  return res.json()
}

export async function deletePipeline(id: string): Promise<void> {
  const res = await fetch(`${BASE}/pipelines/${id}`, { method: 'DELETE' })
  await throwOnError(res)
}

export async function runPipeline(id: string, req?: TriggerRunRequest): Promise<{ id: string }> {
  const res = await fetch(`${BASE}/pipelines/${id}/run`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req ?? {}),
  })
  await throwOnError(res)
  return res.json()
}

export async function deployPipeline(id: string, req: DeployRequest): Promise<unknown> {
  const res = await fetch(`${BASE}/pipelines/${id}/deploy`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ enabled: true, ...req }),
  })
  await throwOnError(res)
  return res.json()
}
