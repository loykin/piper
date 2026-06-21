import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import * as api from './api'
import { useProjectId } from '@/lib/projectContext'
import { useWorkers } from '@/features/workers/hooks'

export const servingKeys = {
  all: (projectId: string) => ['serving', projectId] as const,
  list: (projectId: string) => ['serving', projectId, 'list'] as const,
  one: (projectId: string, name: string) => ['serving', projectId, name] as const,
  history: (projectId: string) => ['serving', projectId, 'history'] as const,
}

export function useServices() {
  const projectId = useProjectId()
  return useQuery({
    queryKey: servingKeys.list(projectId),
    queryFn: () => api.listServing(projectId),
    enabled: !!projectId,
    refetchInterval: 5000,
    notifyOnChangeProps: ['data', 'isLoading'],
  })
}

export function useService(name: string) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: servingKeys.one(projectId, name),
    queryFn: () => api.getServing(projectId, name),
    enabled: !!projectId && !!name,
    refetchInterval: 5000,
    notifyOnChangeProps: ['data', 'isLoading'],
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

export function useServingWorkers() {
  return useWorkers('serving')
}
