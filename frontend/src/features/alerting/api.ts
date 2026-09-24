import { projectApi, type Paged } from '@/lib/api'
import type { AlertRule, CreateAlertRuleRequest, PatchAlertRuleRequest } from './types'

export async function listAlertRules(projectId: string, limit: number, offset: number): Promise<Paged<AlertRule>> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  return projectApi(projectId).getPaged<AlertRule>(`/alert-rules?${params}`)
}

export function getAlertRule(projectId: string, id: string): Promise<AlertRule> {
  return projectApi(projectId).get<AlertRule>(`/alert-rules/${encodeURIComponent(id)}`)
}

export function createAlertRule(projectId: string, request: CreateAlertRuleRequest): Promise<AlertRule> {
  return projectApi(projectId).post<AlertRule>('/alert-rules', request)
}
export function patchAlertRule(projectId: string, id: string, request: PatchAlertRuleRequest): Promise<AlertRule> {
  return projectApi(projectId).patch<AlertRule>(`/alert-rules/${encodeURIComponent(id)}`, request)
}
export function deleteAlertRule(projectId: string, id: string): Promise<void> {
  return projectApi(projectId).delete(`/alert-rules/${encodeURIComponent(id)}`)
}
