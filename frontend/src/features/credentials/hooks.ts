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
  listPaged: (projectId: string, limit: number, offset: number) => ['credentials', projectId, 'list', limit, offset] as const,
}

export function useCredentials() {
  const projectId = useProjectId()
  return useQuery({
    queryKey: credentialKeys.list(projectId),
    queryFn: () => api.listCredentials(projectId),
    enabled: !!projectId,
  })
}

/** Like `useCredentials`, but for a `limit`-paginated page — also returns `total`. */
export function useCredentialsPaged(limit: number, offset: number) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: credentialKeys.listPaged(projectId, limit, offset),
    queryFn: () => api.listCredentialsPaged(projectId, limit, offset),
    enabled: !!projectId,
    placeholderData: (prev) => prev,
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
