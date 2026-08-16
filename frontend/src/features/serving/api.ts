export type { Service, ServiceHistory } from './types'

import type { Service, ServiceHistory } from './types'
import { projectApi } from '@/lib/api'

export async function listServing(projectId: string): Promise<Service[]> {
  const data = await projectApi(projectId).get<Service[]>('/services')
  return Array.isArray(data) ? data : []
}

/**
 * Like `listServing`, but for a `limit`-paginated page — also returns the
 * total row count matching the filter (ignoring limit/offset), read from the
 * `X-Total-Count` response header the server only sets when a limit was sent.
 */
export async function listServingPaged(projectId: string, limit: number, offset: number): Promise<{ services: Service[]; total: number }> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  const { data, total } = await projectApi(projectId).getWithTotal<Service[]>(`/services?${params.toString()}`)
  return { services: Array.isArray(data) ? data : [], total: total ?? 0 }
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

/** Like `listServingHistory`, but for a `limit`-paginated page — see `listServingPaged`. */
export async function listServingHistoryPaged(projectId: string, limit: number, offset: number): Promise<{ history: ServiceHistory[]; total: number }> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  const { data, total } = await projectApi(projectId).getWithTotal<ServiceHistory[]>(`/services/history?${params.toString()}`)
  return { history: Array.isArray(data) ? data : [], total: total ?? 0 }
}

/** Browser predict proxy URL — /projects/:id/services/predict/* */
export function servingPredictURL(projectId: string, path = ''): string {
  return `/projects/${encodeURIComponent(projectId)}/services/predict/${path}`
}
