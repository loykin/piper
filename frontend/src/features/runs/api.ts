// runs feature API
export type {
  Run, Step, LogLine, CreateRunOptions,
  ArtifactFile, ArtifactEntry, StepArtifacts, RunFilter,
  SweepRequest, SweepResponse, RunMetrics,
} from './types'

import type { Run, Step, LogLine, StepArtifacts, RunFilter, SweepRequest, SweepResponse, RunMetrics } from './types'
import { projectApi } from '@/lib/api'

export async function listRuns(projectId: string, filter?: RunFilter): Promise<Run[]> {
  const params = new URLSearchParams()
  if (filter?.status) params.set('status', filter.status)
  if (filter?.pipeline) params.set('pipeline_name', filter.pipeline)
  if (filter?.experiment) params.set('experiment', filter.experiment)
  if (filter?.metric_step) params.set('metric_step', filter.metric_step)
  if (filter?.metric_key) params.set('metric_key', filter.metric_key)
  if (filter?.metric_order) params.set('metric_order', filter.metric_order)
  const qs = params.toString()
  const data = await projectApi(projectId).get<Run[]>(`/runs${qs ? `?${qs}` : ''}`)
  return Array.isArray(data) ? data : []
}

export async function createRun(projectId: string, yaml: string, params?: Record<string, unknown>): Promise<{ run_id: string }> {
  return projectApi(projectId).post<{ run_id: string }>('/runs', { yaml, params })
}

export async function createSweep(projectId: string, req: SweepRequest): Promise<SweepResponse> {
  return projectApi(projectId).post<SweepResponse>('/runs/sweep', req)
}

export async function getRun(projectId: string, id: string): Promise<Run> {
  return projectApi(projectId).get<Run>(`/runs/${id}`)
}

export async function getRunSteps(projectId: string, runID: string): Promise<Step[]> {
  const data = await projectApi(projectId).get<Step[]>(`/runs/${runID}/steps`)
  return Array.isArray(data) ? data : []
}

export async function cancelRun(projectId: string, id: string): Promise<void> {
  return projectApi(projectId).post(`/runs/${id}/cancel`)
}

export async function rerunRun(projectId: string, id: string): Promise<{ run_id: string }> {
  return projectApi(projectId).post<{ run_id: string }>(`/runs/${id}/rerun`)
}

export async function deleteRun(projectId: string, id: string): Promise<void> {
  return projectApi(projectId).delete(`/runs/${id}`)
}

export async function getRunLogs(
  projectId: string,
  runID: string,
  step: string,
  afterID?: number,
): Promise<LogLine[]> {
  const params = afterID != null ? `?after_id=${afterID}` : ''
  const data = await projectApi(projectId).get<LogLine[]>(
    `/runs/${runID}/steps/${step}/logs${params}`,
  )
  return Array.isArray(data) ? data : []
}

export function runLogsStreamURL(projectId: string, runID: string, step: string): string {
  return `/api/projects/${encodeURIComponent(projectId)}/runs/${runID}/steps/${step}/logs/stream`
}

export async function listArtifacts(projectId: string, runID: string): Promise<StepArtifacts[]> {
  const data = await projectApi(projectId).get<StepArtifacts[]>(`/runs/${runID}/artifacts`)
  return Array.isArray(data) ? data : []
}

export function artifactDownloadURL(
  projectId: string,
  runID: string,
  step: string,
  path: string,
): string {
  return `/api/projects/${encodeURIComponent(projectId)}/runs/${runID}/artifacts/${step}/${path}`
}

export async function retryStep(
  projectId: string,
  runID: string,
  step: string,
): Promise<{ run_id: string }> {
  return projectApi(projectId).post<{ run_id: string }>(`/runs/${runID}/steps/${step}/retry`)
}

export async function getRunMetrics(projectId: string, runID: string): Promise<RunMetrics> {
  return projectApi(projectId).get<RunMetrics>(`/runs/${runID}/metrics`)
}

/** SSE event stream URL, filtered to a specific project when projectId is provided. */
export function eventsStreamURL(projectId?: string): string {
  return projectId ? `/events?project_id=${encodeURIComponent(projectId)}` : '/events'
}
