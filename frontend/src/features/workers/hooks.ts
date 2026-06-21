// workers feature hooks — React Query wrappers
import { useMemo } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import * as api from './api'
import type { Worker } from './types'

export const workerKeys = {
  all: ['workers'] as const,
  list: () => ['workers', 'list'] as const,
}

export const workerPolicyKeys = {
  all:  ['workers', 'pod-policies'] as const,
  list: () => ['workers', 'pod-policies', 'list'] as const,
  one:  (id: string) => ['workers', 'pod-policy', id] as const,
}

type WorkerCapability = Worker['capabilities'][number]

export function useWorkers(capability?: WorkerCapability) {
  const select = useMemo(
    () => capability
      ? (workers: Worker[]) => workers.filter(worker => worker.capabilities.includes(capability))
      : undefined,
    [capability],
  )
  return useQuery({
    queryKey: workerKeys.list(),
    queryFn: api.listWorkers,
    select,
    refetchInterval: 5000,
    notifyOnChangeProps: ['data', 'isLoading'],
  })
}

export function useWorkerPodPolicies() {
  return useQuery({
    queryKey: workerPolicyKeys.list(),
    queryFn: api.listWorkerPodPolicies,
  })
}

export function useWorkerPodPolicy(workerId: string) {
  return useQuery({
    queryKey: workerPolicyKeys.one(workerId),
    queryFn: () => api.getWorkerPodPolicy(workerId),
    enabled: !!workerId,
  })
}

export function useSetWorkerPodPolicy() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ workerId, yaml }: { workerId: string; yaml: string }) =>
      api.setWorkerPodPolicy(workerId, yaml),
    onSuccess: (_, { workerId }) => {
      void qc.invalidateQueries({ queryKey: workerPolicyKeys.one(workerId) })
      void qc.invalidateQueries({ queryKey: workerPolicyKeys.all })
    },
  })
}

export function useDeleteWorkerPodPolicy() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (workerId: string) => api.deleteWorkerPodPolicy(workerId),
    onSuccess: (_, workerId) => {
      void qc.invalidateQueries({ queryKey: workerPolicyKeys.one(workerId) })
      void qc.invalidateQueries({ queryKey: workerPolicyKeys.all })
    },
  })
}
