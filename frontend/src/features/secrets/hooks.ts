import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useProjectId } from '@/lib/projectContext'
import * as api from './api'
import type { CreateSecretRequest, PatchSecretRequest, RotateSecretRequest } from './types'

export const secretKeys = {
  all: (projectId: string) => ['secrets', projectId] as const,
  list: (projectId: string) => ['secrets', projectId, 'list'] as const,
}

export function useSecrets() {
  const projectId = useProjectId()
  return useQuery({
    queryKey: secretKeys.list(projectId),
    queryFn: () => api.listSecrets(projectId),
    enabled: !!projectId,
  })
}

export function useCreateSecret() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (req: CreateSecretRequest) => api.createSecret(projectId, req),
    onSuccess: () => qc.invalidateQueries({ queryKey: secretKeys.all(projectId) }),
  })
}

export function useRotateSecret() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ name, data }: { name: string; data: RotateSecretRequest['data'] }) =>
      api.rotateSecret(projectId, name, { data }),
    onSuccess: () => qc.invalidateQueries({ queryKey: secretKeys.all(projectId) }),
  })
}

export function usePatchSecret() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ name, patch }: { name: string; patch: PatchSecretRequest }) =>
      api.patchSecret(projectId, name, patch),
    onSuccess: () => qc.invalidateQueries({ queryKey: secretKeys.all(projectId) }),
  })
}

export function useDeleteSecret() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => api.deleteSecret(projectId, name),
    onSuccess: () => qc.invalidateQueries({ queryKey: secretKeys.all(projectId) }),
  })
}
