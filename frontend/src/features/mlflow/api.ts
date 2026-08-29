import { projectApi } from '@/lib/api'
import type { MLflowIntegration, MLflowIntegrationDetail, MLflowIntegrationRequest, MLflowRunLink, MLflowTestResult } from './types'

export async function listIntegrations(projectId: string, limit: number, offset: number) {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  const { data, total } = await projectApi(projectId).getWithTotal<MLflowIntegrationDetail[]>(`/mlflow-integrations?${params}`)
  return { integrations: Array.isArray(data) ? data : [], total: total ?? 0 }
}
export function getIntegration(projectId: string, id: string) { return projectApi(projectId).get<MLflowIntegrationDetail>(`/mlflow-integrations/${encodeURIComponent(id)}`) }
export function createIntegration(projectId: string, value: MLflowIntegrationRequest) { return projectApi(projectId).post<MLflowIntegration>('/mlflow-integrations', value) }
export function updateIntegration(projectId: string, id: string, value: MLflowIntegrationRequest) { return projectApi(projectId).put<MLflowIntegration>(`/mlflow-integrations/${encodeURIComponent(id)}`, value) }
export function deleteIntegration(projectId: string, id: string) { return projectApi(projectId).delete(`/mlflow-integrations/${encodeURIComponent(id)}`) }
export function testIntegration(projectId: string, id: string) { return projectApi(projectId).post<MLflowTestResult>(`/mlflow-integrations/${encodeURIComponent(id)}/test`) }
export async function listRunLinks(projectId: string, runId: string) {
  const data = await projectApi(projectId).get<MLflowRunLink[]>(`/runs/${encodeURIComponent(runId)}/mlflow-links`)
  return Array.isArray(data) ? data : []
}
