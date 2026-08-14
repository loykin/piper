export type { Service, ServiceHistory } from './types'

import type { Service, ServiceHistory } from './types'
import { projectApi } from '@/lib/api'

export async function listServing(projectId: string): Promise<Service[]> {
  const data = await projectApi(projectId).get<Service[]>('/services')
  return Array.isArray(data) ? data : []
}

export async function getServing(projectId: string, name: string): Promise<Service> {
  return projectApi(projectId).get<Service>(`/services/${name}`)
}

export async function createServing(
  projectId: string,
  yaml: string,
): Promise<{ name: string }> {
  return projectApi(projectId).post<{ name: string }>('/services', { yaml })
}

export async function stopServing(projectId: string, name: string): Promise<void> {
  return projectApi(projectId).delete(`/services/${name}`)
}

export async function restartServing(projectId: string, name: string): Promise<void> {
  return projectApi(projectId).post(`/services/${name}/restart`)
}

export async function listServingHistory(projectId: string): Promise<ServiceHistory[]> {
  const data = await projectApi(projectId).get<ServiceHistory[]>('/services/history')
  return Array.isArray(data) ? data : []
}

/** Browser predict proxy URL — /projects/:id/services/predict/* */
export function servingPredictURL(projectId: string, path = ''): string {
  return `/projects/${encodeURIComponent(projectId)}/services/predict/${path}`
}
