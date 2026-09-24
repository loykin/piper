export type {
  PipelineTemplate, CreatePipelineRequest, TriggerRunRequest,
} from './types'

import type {
  PipelineTemplate, CreatePipelineRequest, TriggerRunRequest,
} from './types'
import { projectApi, type Paged } from '@/lib/api'

export async function listPipelines(
  projectId: string,
  name?: string,
  limit?: number,
): Promise<PipelineTemplate[]> {
  const params = new URLSearchParams()
  if (name) params.set('name', name)
  if (limit) params.set('limit', String(limit))
  const qs = params.size > 0 ? `?${params.toString()}` : ''
  return projectApi(projectId).getList<PipelineTemplate>(`/pipeline-templates${qs}`)
}

/**
 * Like `listPipelines`, but for a `limit`-paginated page — also returns the
 * total row count matching `name` (ignoring limit/offset), read from the
 * `X-Total-Count` response header the server only sets when a limit was sent.
 */
export async function listPipelinesPaged(
  projectId: string,
  name: string | undefined,
  limit: number,
  offset: number,
): Promise<Paged<PipelineTemplate>> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  if (name) params.set('name', name)
  return projectApi(projectId).getPaged<PipelineTemplate>(`/pipeline-templates?${params.toString()}`)
}

export async function createPipeline(
  projectId: string,
  req: CreatePipelineRequest,
): Promise<PipelineTemplate> {
  return projectApi(projectId).post<PipelineTemplate>('/pipeline-templates', req)
}

export async function getPipeline(projectId: string, id: string): Promise<PipelineTemplate> {
  return projectApi(projectId).get<PipelineTemplate>(`/pipeline-templates/${encodeURIComponent(id)}`)
}

export async function deletePipeline(projectId: string, id: string): Promise<void> {
  return projectApi(projectId).delete(`/pipeline-templates/${encodeURIComponent(id)}`)
}

export async function runPipeline(
  projectId: string,
  id: string,
  req?: TriggerRunRequest,
): Promise<{ id: string }> {
  return projectApi(projectId).post<{ id: string }>(`/pipeline-templates/${encodeURIComponent(id)}/run`, req ?? {})
}

