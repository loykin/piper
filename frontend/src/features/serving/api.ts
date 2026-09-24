export type { Service, ServiceHistory } from './types'

import type { Service, ServiceHistory } from './types'
import { projectApi, type Paged } from '@/lib/api'

export async function listServing(projectId: string): Promise<Service[]> {
  return projectApi(projectId).getList<Service>('/services')
}

/**
 * Like `listServing`, but for a `limit`-paginated page — also returns the
 * total row count matching the filter (ignoring limit/offset), read from the
 * `X-Total-Count` response header the server only sets when a limit was sent.
 */
export async function listServingPaged(projectId: string, limit: number, offset: number): Promise<Paged<Service>> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  return projectApi(projectId).getPaged<Service>(`/services?${params.toString()}`)
}

export async function getServing(projectId: string, name: string): Promise<Service> {
  return projectApi(projectId).get<Service>(`/services/${encodeURIComponent(name)}`)
}

export async function createServing(
  projectId: string,
  yaml: string,
): Promise<{ name: string }> {
  return projectApi(projectId).post<{ name: string }>('/services', { yaml })
}

export async function stopServing(projectId: string, name: string): Promise<void> {
  return projectApi(projectId).delete(`/services/${encodeURIComponent(name)}`)
}

export async function restartServing(projectId: string, name: string): Promise<void> {
  return projectApi(projectId).post(`/services/${encodeURIComponent(name)}/restart`)
}

export async function listServingHistory(projectId: string): Promise<ServiceHistory[]> {
  return projectApi(projectId).getList<ServiceHistory>('/services/history')
}

/** Like `listServingHistory`, but for a `limit`-paginated page — see `listServingPaged`. */
export async function listServingHistoryPaged(projectId: string, limit: number, offset: number): Promise<Paged<ServiceHistory>> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  return projectApi(projectId).getPaged<ServiceHistory>(`/services/history?${params.toString()}`)
}

/** Browser predict proxy URL — /projects/:id/services/predict/* */
export function servingPredictURL(projectId: string, path = ''): string {
  return `/projects/${encodeURIComponent(projectId)}/services/predict/${path}`
}
