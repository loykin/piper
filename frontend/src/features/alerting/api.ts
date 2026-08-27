import { projectApi } from '@/lib/api'
import type { AlertRule, CreateAlertRuleRequest, PatchAlertRuleRequest } from './types'

export async function listAlertRules(projectId: string, limit: number, offset: number): Promise<{ rules: AlertRule[]; total: number }> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  const { data, total } = await projectApi(projectId).getWithTotal<AlertRule[]>(`/alert-rules?${params}`)
  return { rules: Array.isArray(data) ? data : [], total: total ?? 0 }
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
