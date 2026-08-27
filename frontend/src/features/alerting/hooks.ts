import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useProjectId } from '@/lib/projectContext'
import * as api from './api'
import type { CreateAlertRuleRequest, PatchAlertRuleRequest } from './types'

export const alertRuleKeys = {
  all: (projectId: string) => ['alert-rules', projectId] as const,
  list: (projectId: string, limit: number, offset: number) => ['alert-rules', projectId, 'list', limit, offset] as const,
}

export function useAlertRules(limit: number, offset: number) {
  const projectId = useProjectId()
  return useQuery({ queryKey: alertRuleKeys.list(projectId, limit, offset), queryFn: () => api.listAlertRules(projectId, limit, offset), enabled: !!projectId, placeholderData: previous => previous })
}
export function useCreateAlertRule() {
  const projectId = useProjectId(); const qc = useQueryClient()
  return useMutation({ mutationFn: (request: CreateAlertRuleRequest) => api.createAlertRule(projectId, request), onSuccess: () => qc.invalidateQueries({ queryKey: alertRuleKeys.all(projectId) }) })
}
export function usePatchAlertRule() {
  const projectId = useProjectId(); const qc = useQueryClient()
  return useMutation({ mutationFn: ({ id, request }: { id: string; request: PatchAlertRuleRequest }) => api.patchAlertRule(projectId, id, request), onSuccess: () => qc.invalidateQueries({ queryKey: alertRuleKeys.all(projectId) }) })
}
export function useDeleteAlertRule() {
  const projectId = useProjectId(); const qc = useQueryClient()
  return useMutation({ mutationFn: (id: string) => api.deleteAlertRule(projectId, id), onSuccess: () => qc.invalidateQueries({ queryKey: alertRuleKeys.all(projectId) }) })
}
