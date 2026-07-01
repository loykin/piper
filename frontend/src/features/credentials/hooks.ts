import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useProjectId } from '@/lib/projectContext'
import * as api from './api'
import type {
  CreateCredentialRequest,
  PatchCredentialRequest,
  RotateCredentialRequest,
  TestCredentialRequest,
} from './types'

export const credentialKeys = {
  all: (projectId: string) => ['credentials', projectId] as const,
  list: (projectId: string) => ['credentials', projectId, 'list'] as const,
}

export function useCredentials() {
  const projectId = useProjectId()
  return useQuery({
    queryKey: credentialKeys.list(projectId),
    queryFn: () => api.listCredentials(projectId),
    enabled: !!projectId,
  })
}

export function useCreateCredential() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (req: CreateCredentialRequest) => api.createCredential(projectId, req),
    onSuccess: () => qc.invalidateQueries({ queryKey: credentialKeys.all(projectId) }),
  })
}

export function useRotateCredential() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ name, data }: { name: string; data: RotateCredentialRequest['data'] }) =>
      api.rotateCredential(projectId, name, { data }),
    onSuccess: () => qc.invalidateQueries({ queryKey: credentialKeys.all(projectId) }),
  })
}

export function usePatchCredential() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ name, patch }: { name: string; patch: PatchCredentialRequest }) =>
      api.patchCredential(projectId, name, patch),
    onSuccess: () => qc.invalidateQueries({ queryKey: credentialKeys.all(projectId) }),
  })
}

export function useTestCredential() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ name, req }: { name: string; req: TestCredentialRequest }) =>
      api.testCredential(projectId, name, req),
    onSettled: () => qc.invalidateQueries({ queryKey: credentialKeys.all(projectId) }),
  })
}

export function useDeleteCredential() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => api.deleteCredential(projectId, name),
    onSuccess: () => qc.invalidateQueries({ queryKey: credentialKeys.all(projectId) }),
  })
}

// ── System-scoped credential hooks (admin) ─────────────────────────────────────

export const systemCredentialKeys = {
  all: ['credentials', 'system'] as const,
  list: ['credentials', 'system', 'list'] as const,
}

export function useSystemCredentials() {
  return useQuery({
    queryKey: systemCredentialKeys.list,
    queryFn: api.listSystemCredentials,
  })
}

export function useCreateSystemCredential() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (req: CreateCredentialRequest) => api.createSystemCredential(req),
    onSuccess: () => qc.invalidateQueries({ queryKey: systemCredentialKeys.all }),
  })
}

export function useRotateSystemCredential() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ name, data }: { name: string; data: RotateCredentialRequest['data'] }) =>
      api.rotateSystemCredential(name, { data }),
    onSuccess: () => qc.invalidateQueries({ queryKey: systemCredentialKeys.all }),
  })
}

export function useDeleteSystemCredential() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => api.deleteSystemCredential(name),
    onSuccess: () => qc.invalidateQueries({ queryKey: systemCredentialKeys.all }),
  })
}
