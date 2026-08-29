// runs feature API
export type {
  Run, RunDetail, Step, LogLine, CreateRunOptions,
  ArtifactFile, ArtifactEntry, StepArtifacts, RunFilter,
  SweepRequest, SweepResponse, RunMetric, RunMetrics, StatsCapabilities,
} from './types'

import type { Run, RunDetail, Step, LogLine, StepArtifacts, RunFilter, SweepRequest, SweepResponse, RunMetric, RunMetrics, StatsCapabilities } from './types'
import { projectApi } from '@/lib/api'

function runListParams(filter?: RunFilter): URLSearchParams {
  const params = new URLSearchParams()
  if (filter?.status) params.set('status', filter.status)
  if (filter?.pipeline) params.set('pipeline_name', filter.pipeline)
  if (filter?.experiment) params.set('experiment', filter.experiment)
  if (filter?.metric_step) params.set('metric_step', filter.metric_step)
  if (filter?.metric_key) params.set('metric_key', filter.metric_key)
  if (filter?.metric_order) params.set('metric_order', filter.metric_order)
  if (filter?.schedule_id) params.set('schedule_id', filter.schedule_id)
  if (filter?.include_steps) params.set('include_steps', 'true')
  if (filter?.limit) params.set('limit', String(filter.limit))
  if (filter?.offset) params.set('offset', String(filter.offset))
  return params
}

export async function listRuns(projectId: string, filter?: RunFilter): Promise<Run[]> {
  const qs = runListParams(filter).toString()
  const data = await projectApi(projectId).get<Run[]>(`/runs${qs ? `?${qs}` : ''}`)
  return Array.isArray(data) ? data : []
}

/**
 * Like `listRuns`, but for `filter.limit`-paginated views: also returns the
 * total row count matching the filter (ignoring limit/offset), read from the
 * `X-Total-Count` response header the server only sets when a limit was sent.
 */
export async function listRunsPaged(projectId: string, filter: RunFilter): Promise<{ runs: Run[]; total: number }> {
  const qs = runListParams(filter).toString()
  const { data, total } = await projectApi(projectId).getWithTotal<Run[]>(`/runs${qs ? `?${qs}` : ''}`)
  return { runs: Array.isArray(data) ? data : [], total: total ?? 0 }
}

export async function createRun(projectId: string, yaml: string, params?: Record<string, unknown>): Promise<{ run_id: string }> {
  return projectApi(projectId).post<{ run_id: string }>('/runs', { yaml, params })
}

export async function createSweep(projectId: string, req: SweepRequest): Promise<SweepResponse> {
  return projectApi(projectId).post<SweepResponse>('/runs/sweep', req)
}

export async function getRun(projectId: string, id: string): Promise<Run> {
  const data = await projectApi(projectId).get<RunDetail>(`/runs/${id}`)
  return data.run
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

export interface RunLogPageOptions {
  cursor?: string
  limit?: number
  since?: string
  until?: string
}

export async function getRunLogPage(
  projectId: string,
  runID: string,
  step: string,
  options: RunLogPageOptions = {},
): Promise<{ lines: LogLine[]; nextCursor: string | null }> {
  const params = new URLSearchParams()
  if (options.cursor) params.set('cursor', options.cursor)
  if (options.limit) params.set('limit', String(options.limit))
  if (options.since) params.set('since', options.since)
  if (options.until) params.set('until', options.until)
  const query = params.toString()
  const response = await projectApi(projectId).getWithCursor<LogLine[]>(
    `/runs/${runID}/steps/${step}/logs${query ? `?${query}` : ''}`,
  )
  return { lines: Array.isArray(response.data) ? response.data : [], nextCursor: response.nextCursor }
}

export async function getStatsCapabilities(projectId: string): Promise<StatsCapabilities> {
  return projectApi(projectId).get<StatsCapabilities>('/stats/capabilities')
}

export function runLogsStreamURL(projectId: string, runID: string, step: string, cursor?: string): string {
  const base = `/api/projects/${encodeURIComponent(projectId)}/runs/${runID}/steps/${step}/logs/stream`
  return cursor ? `${base}?cursor=${encodeURIComponent(cursor)}` : base
}

export async function listArtifacts(projectId: string, runID: string): Promise<StepArtifacts[]> {
  const data = await projectApi(projectId).get<StepArtifacts[]>(`/runs/${runID}/artifacts`)
  return Array.isArray(data) ? data : []
}

export interface ArtifactDownloadURLParams {
  projectId: string
  runId: string
  step: string
  artifact: string
  filePath: string
}

export function artifactDownloadURL({
  projectId,
  runId,
  step,
  artifact,
  filePath,
}: ArtifactDownloadURLParams): string {
  const path = [artifact, filePath]
    .flatMap(part => part.split('/'))
    .map(encodeURIComponent)
    .join('/')
  return `/api/projects/${encodeURIComponent(projectId)}/runs/${encodeURIComponent(runId)}/artifacts/${encodeURIComponent(step)}/${path}`
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

export interface RunMetricPageOptions {
  cursor?: string
  limit?: number
  step?: string
  keys?: string[]
  since?: string
  until?: string
}

export async function getRunMetricPage(
  projectId: string,
  runID: string,
  options: RunMetricPageOptions = {},
): Promise<{ points: RunMetric[]; nextCursor: string | null }> {
  const params = new URLSearchParams()
  if (options.cursor) params.set('cursor', options.cursor)
  if (options.limit) params.set('limit', String(options.limit))
  if (options.step) params.set('step', options.step)
  for (const key of options.keys ?? []) params.append('key', key)
  if (options.since) params.set('since', options.since)
  if (options.until) params.set('until', options.until)
  const query = params.toString()
  const response = await projectApi(projectId).getWithCursor<RunMetric[]>(
    `/runs/${runID}/metrics${query ? `?${query}` : ''}`,
  )
  return { points: Array.isArray(response.data) ? response.data : [], nextCursor: response.nextCursor }
}

/** SSE event stream URL, filtered to a specific project when projectId is provided. */
export function eventsStreamURL(projectId?: string): string {
  return projectId ? `/events?project_id=${encodeURIComponent(projectId)}` : '/events'
}
