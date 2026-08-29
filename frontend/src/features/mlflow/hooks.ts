import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useProjectId } from '@/lib/projectContext'
import * as api from './api'
import type { MLflowIntegrationRequest } from './types'

export const mlflowKeys = {
  all: (projectId: string) => ['mlflow', projectId] as const,
  list: (projectId: string, limit: number, offset: number) => ['mlflow', projectId, 'list', limit, offset] as const,
  detail: (projectId: string, id: string) => ['mlflow', projectId, 'detail', id] as const,
  run: (projectId: string, runId: string) => ['mlflow', projectId, 'run', runId] as const,
}
export function useMLflowIntegrations(limit: number, offset: number) { const projectId = useProjectId(); return useQuery({ queryKey: mlflowKeys.list(projectId, limit, offset), queryFn: () => api.listIntegrations(projectId, limit, offset), enabled: !!projectId, placeholderData: previous => previous }) }
export function useMLflowIntegration(id: string) { const projectId = useProjectId(); return useQuery({ queryKey: mlflowKeys.detail(projectId, id), queryFn: () => api.getIntegration(projectId, id), enabled: !!projectId && !!id }) }
export function useCreateMLflowIntegration() { const projectId = useProjectId(); const client = useQueryClient(); return useMutation({ mutationFn: (value: MLflowIntegrationRequest) => api.createIntegration(projectId, value), onSuccess: () => client.invalidateQueries({ queryKey: mlflowKeys.all(projectId) }) }) }
export function useUpdateMLflowIntegration(id: string) { const projectId = useProjectId(); const client = useQueryClient(); return useMutation({ mutationFn: (value: MLflowIntegrationRequest) => api.updateIntegration(projectId, id, value), onSuccess: () => client.invalidateQueries({ queryKey: mlflowKeys.all(projectId) }) }) }
export function useDeleteMLflowIntegration() { const projectId = useProjectId(); const client = useQueryClient(); return useMutation({ mutationFn: (id: string) => api.deleteIntegration(projectId, id), onSuccess: () => client.invalidateQueries({ queryKey: mlflowKeys.all(projectId) }) }) }
export function useTestMLflowIntegration() { const projectId = useProjectId(); const client = useQueryClient(); return useMutation({ mutationFn: (id: string) => api.testIntegration(projectId, id), onSettled: (_data, _error, id) => client.invalidateQueries({ queryKey: mlflowKeys.detail(projectId, id) }) }) }
export function useMLflowRunLinks(runId: string) { const projectId = useProjectId(); return useQuery({ queryKey: mlflowKeys.run(projectId, runId), queryFn: () => api.listRunLinks(projectId, runId), enabled: !!projectId && !!runId, refetchInterval: query => query.state.data?.some(link => ['pending', 'syncing'].includes(link.sync_status)) ? 3000 : false }) }
