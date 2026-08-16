import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import * as api from './api'
import { useProjectId } from '@/lib/projectContext'
import { backgroundPolling } from '@/lib/query'

export const servingKeys = {
  all: (projectId: string) => ['serving', projectId] as const,
  list: (projectId: string) => ['serving', projectId, 'list'] as const,
  listPaged: (projectId: string, limit: number, offset: number) => ['serving', projectId, 'list', limit, offset] as const,
  one: (projectId: string, name: string) => ['serving', projectId, name] as const,
  history: (projectId: string) => ['serving', projectId, 'history'] as const,
  historyPaged: (projectId: string, limit: number, offset: number) => ['serving', projectId, 'history', limit, offset] as const,
}

export function useServices() {
  const projectId = useProjectId()
  return useQuery({
    queryKey: servingKeys.list(projectId),
    queryFn: () => api.listServing(projectId),
    enabled: !!projectId,
    ...backgroundPolling(5000),
  })
}

/** Like `useServices`, but for a `limit`-paginated page — also returns `total`. */
export function useServicesPaged(limit: number, offset: number) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: servingKeys.listPaged(projectId, limit, offset),
    queryFn: () => api.listServingPaged(projectId, limit, offset),
    enabled: !!projectId,
    placeholderData: (prev) => prev,
    ...backgroundPolling(5000),
  })
}

export function useService(name: string) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: servingKeys.one(projectId, name),
    queryFn: () => api.getServing(projectId, name),
    enabled: !!projectId && !!name,
    ...backgroundPolling(5000),
  })
}

export function useServingHistory() {
  const projectId = useProjectId()
  return useQuery({
    queryKey: servingKeys.history(projectId),
    queryFn: () => api.listServingHistory(projectId),
    enabled: !!projectId,
  })
}

/** Like `useServingHistory`, but for a `limit`-paginated page — also returns `total`. */
export function useServingHistoryPaged(limit: number, offset: number) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: servingKeys.historyPaged(projectId, limit, offset),
    queryFn: () => api.listServingHistoryPaged(projectId, limit, offset),
    enabled: !!projectId,
    placeholderData: (prev) => prev,
  })
}

export function useCreateService() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (yaml: string) => api.createServing(projectId, yaml),
    onSuccess: () => qc.invalidateQueries({ queryKey: servingKeys.all(projectId) }),
  })
}

export function useStopService() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => api.stopServing(projectId, name),
    onSuccess: () => qc.invalidateQueries({ queryKey: servingKeys.all(projectId) }),
  })
}

export function useRestartService() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => api.restartServing(projectId, name),
    onSuccess: () => qc.invalidateQueries({ queryKey: servingKeys.all(projectId) }),
  })
}
