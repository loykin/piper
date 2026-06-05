// serving feature hooks — React Query wrappers
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import * as api from './api'

export const servingKeys = {
  all: ['serving'] as const,
  list: () => ['serving', 'list'] as const,
  one: (name: string) => ['serving', name] as const,
  history: () => ['serving', 'history'] as const,
  workers: () => ['serving-workers'] as const,
}

export function useServices() {
  return useQuery({
    queryKey: servingKeys.list(),
    queryFn: api.listServing,
    refetchInterval: 5000,
  })
}

export function useService(name: string) {
  return useQuery({
    queryKey: servingKeys.one(name),
    queryFn: () => api.getServing(name),
    refetchInterval: 5000,
  })
}

export function useServingHistory() {
  return useQuery({
    queryKey: servingKeys.history(),
    queryFn: api.listServingHistory,
  })
}

export function useServingWorkers() {
  return useQuery({
    queryKey: servingKeys.workers(),
    queryFn: api.listServingWorkers,
    refetchInterval: 10000,
  })
}

export function useDeployService() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.deployServing,
    onSuccess: () => qc.invalidateQueries({ queryKey: servingKeys.all }),
  })
}

export function useStopService() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.stopServing,
    onSuccess: () => qc.invalidateQueries({ queryKey: servingKeys.all }),
  })
}

export function useRestartService() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.restartServing,
    onSuccess: () => qc.invalidateQueries({ queryKey: servingKeys.all }),
  })
}
