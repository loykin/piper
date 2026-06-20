export type { Service, ServiceHistory, ServingWorkerInfo } from './types'

import type { Service, ServiceHistory } from './types'
import { projectApi } from '@/lib/api'

export async function listServing(projectId: string): Promise<Service[]> {
  const data = await projectApi(projectId).get<Service[]>('/serving')
  return Array.isArray(data) ? data : []
}

export async function getServing(projectId: string, name: string): Promise<Service> {
  return projectApi(projectId).get<Service>(`/serving/${name}`)
}

export async function createServing(
  projectId: string,
  yaml: string,
): Promise<{ name: string }> {
  return projectApi(projectId).post<{ name: string }>('/serving', { yaml })
}

export async function stopServing(projectId: string, name: string): Promise<void> {
  return projectApi(projectId).delete(`/serving/${name}`)
}

export async function restartServing(projectId: string, name: string): Promise<void> {
  return projectApi(projectId).post(`/serving/${name}/restart`)
}

export async function listServingHistory(projectId: string): Promise<ServiceHistory[]> {
  const data = await projectApi(projectId).get<ServiceHistory[]>('/serving/history')
  return Array.isArray(data) ? data : []
}

/** Browser predict proxy URL — /projects/:id/serving/predict/* */
export function servingPredictURL(projectId: string, path = ''): string {
  return `/projects/${encodeURIComponent(projectId)}/serving/predict/${path}`
}
