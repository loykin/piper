// pipelines feature hooks — React Query wrappers
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import * as api from './api'
import type { SubmitPipelineRequest, TriggerRunRequest, DeployRequest } from './types'
import { useProjectId } from '@/lib/projectContext'

export const pipelineKeys = {
  all: (projectId: string) => ['pipelines', projectId] as const,
  list: (projectId: string, name?: string) => ['pipelines', projectId, 'list', name] as const,
  one: (projectId: string, id: string) => ['pipelines', projectId, id] as const,
}

export function usePipelines(name?: string, limit?: number) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: pipelineKeys.list(projectId, name),
    queryFn: () => api.listPipelines(projectId, name, limit),
    enabled: !!projectId,
    refetchInterval: 10000,
  })
}

export function useSubmitPipeline() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (req: SubmitPipelineRequest) => api.submitPipeline(projectId, req),
    onSuccess: () => qc.invalidateQueries({ queryKey: pipelineKeys.all(projectId) }),
  })
}

export function useDeletePipeline() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.deletePipeline(projectId, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: pipelineKeys.all(projectId) }),
  })
}

export function useRunPipeline() {
  const projectId = useProjectId()
  return useMutation({
    mutationFn: ({ id, req }: { id: string; req?: TriggerRunRequest }) =>
      api.runPipeline(projectId, id, req),
  })
}

export function useDeployPipeline() {
  const projectId = useProjectId()
  return useMutation({
    mutationFn: ({ id, req }: { id: string; req: DeployRequest }) =>
      api.deployPipeline(projectId, id, req),
  })
}
