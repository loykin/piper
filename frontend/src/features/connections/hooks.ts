import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useProjectId } from '@/lib/projectContext'
import * as api from './api'
import type { CreateConnectionRequest, PatchConnectionRequest, RotateConnectionRequest, TestConnectionRequest } from './types'

export const connectionKeys = {
  all: (projectId: string) => ['connections', projectId] as const,
  list: (projectId: string) => ['connections', projectId, 'list'] as const,
  one: (projectId: string, name: string) => ['connections', projectId, name] as const,
}

export function useConnections() {
  const projectId = useProjectId()
  return useQuery({
    queryKey: connectionKeys.list(projectId),
    queryFn: () => api.listConnections(projectId),
    enabled: !!projectId,
  })
}

export function useConnection(name: string) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: connectionKeys.one(projectId, name),
    queryFn: () => api.getConnection(projectId, name),
    enabled: !!projectId && !!name,
  })
}

export function useCreateConnection() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (req: CreateConnectionRequest) => api.createConnection(projectId, req),
    onSuccess: () => qc.invalidateQueries({ queryKey: connectionKeys.all(projectId) }),
  })
}

export function useRotateConnection() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ name, data }: { name: string; data: RotateConnectionRequest['data'] }) =>
      api.rotateConnection(projectId, name, { data }),
    onSuccess: () => qc.invalidateQueries({ queryKey: connectionKeys.all(projectId) }),
  })
}

export function usePatchConnection() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ name, patch }: { name: string; patch: PatchConnectionRequest }) =>
      api.patchConnection(projectId, name, patch),
    onSuccess: () => qc.invalidateQueries({ queryKey: connectionKeys.all(projectId) }),
  })
}

export function useTestConnection() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ name, req }: { name: string; req: TestConnectionRequest }) =>
      api.testConnection(projectId, name, req),
    onSettled: () => qc.invalidateQueries({ queryKey: connectionKeys.all(projectId) }),
  })
}

export function useDeleteConnection() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => api.deleteConnection(projectId, name),
    onSuccess: () => qc.invalidateQueries({ queryKey: connectionKeys.all(projectId) }),
  })
}
