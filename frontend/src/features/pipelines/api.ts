export type {
  PipelineTemplate, CreatePipelineRequest, TriggerRunRequest, DeployRequest, UpdateMetaRequest,
} from './types'

import type {
  PipelineTemplate, CreatePipelineRequest, TriggerRunRequest, DeployRequest, UpdateMetaRequest,
} from './types'
import { projectApi } from '@/lib/api'

export async function listPipelines(
  projectId: string,
  name?: string,
  limit?: number,
): Promise<PipelineTemplate[]> {
  const params = new URLSearchParams()
  if (name) params.set('name', name)
  if (limit) params.set('limit', String(limit))
  const qs = params.size > 0 ? `?${params.toString()}` : ''
  const data = await projectApi(projectId).get<PipelineTemplate[]>(`/pipelines${qs}`)
  return Array.isArray(data) ? data : []
}

export async function createPipeline(
  projectId: string,
  req: CreatePipelineRequest,
): Promise<PipelineTemplate> {
  return projectApi(projectId).post<PipelineTemplate>('/pipelines', req)
}

export async function getPipeline(projectId: string, id: string): Promise<PipelineTemplate> {
  return projectApi(projectId).get<PipelineTemplate>(`/pipelines/${id}`)
}

export async function deletePipeline(projectId: string, id: string): Promise<void> {
  return projectApi(projectId).delete(`/pipelines/${id}`)
}

export async function runPipeline(
  projectId: string,
  id: string,
  req?: TriggerRunRequest,
): Promise<{ id: string }> {
  return projectApi(projectId).post<{ id: string }>(`/pipelines/${id}/run`, req ?? {})
}

export async function updateTemplateMeta(
  projectId: string,
  versionId: string,
  req: UpdateMetaRequest,
): Promise<void> {
  return projectApi(projectId).patch(`/pipelines/${versionId}/meta`, req)
}

export async function deployPipeline(
  projectId: string,
  id: string,
  req: DeployRequest,
): Promise<import('@/features/schedules/api').Schedule> {
  return projectApi(projectId).post(`/pipelines/${id}/deploy`, { enabled: true, ...req })
}
