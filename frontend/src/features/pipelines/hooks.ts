// pipelines feature hooks — React Query wrappers
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import * as api from './api'
import type { SubmitPipelineRequest, TriggerRunRequest, DeployRequest } from './types'

export const pipelineKeys = {
  all: ['pipelines'] as const,
  list: (name?: string) => ['pipelines', 'list', name] as const,
  one: (id: string) => ['pipelines', id] as const,
}

export function usePipelines(name?: string, limit?: number) {
  return useQuery({
    queryKey: pipelineKeys.list(name),
    queryFn: () => api.listPipelines(name, limit),
    refetchInterval: 10000,
  })
}

export function useSubmitPipeline() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (req: SubmitPipelineRequest) => api.submitPipeline(req),
    onSuccess: () => qc.invalidateQueries({ queryKey: pipelineKeys.all }),
  })
}

export function useDeletePipeline() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.deletePipeline,
    onSuccess: () => qc.invalidateQueries({ queryKey: pipelineKeys.all }),
  })
}

export function useRunPipeline() {
  return useMutation({
    mutationFn: ({ id, req }: { id: string; req?: TriggerRunRequest }) =>
      api.runPipeline(id, req),
  })
}

export function useDeployPipeline() {
  return useMutation({
    mutationFn: ({ id, req }: { id: string; req: DeployRequest }) =>
      api.deployPipeline(id, req),
  })
}
