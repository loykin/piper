import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import * as api from './api'
import type { StorageConfig } from './types'
import { useProjectId } from '@/lib/projectContext'
import { ApiError } from '@/lib/api'

export const storageKeys = {
  settings: () => ['storage', 'settings'] as const,
  // Genuinely shorter than objects() below — used for broad invalidation so
  // it partial-matches every prefix variant, including the default ''.
  // objects(projectId) alone would end in `undefined`, which does NOT
  // partial-match a live query key ending in '' (React Query compares
  // element-by-element, and undefined !== '').
  objectsAll: (projectId: string) => ['storage', projectId, 'objects'] as const,
  objectsPaged: (projectId: string, limit: number, offset: number, prefix: string) =>
    ['storage', projectId, 'objects', 'paged', limit, offset, prefix] as const,
}

export function useStorageSettings() {
  return useQuery({
    queryKey: storageKeys.settings(),
    queryFn: api.getStorageSettings,
    // 403 here means "not a system admin on this instance" — retrying can
    // never fix that, and unlike a transient 5xx it shouldn't eat a retry
    // budget or leave the query stuck retrying.
    retry: (failureCount, error) => !(error instanceof ApiError && error.status >= 400 && error.status < 500) && failureCount < 1,
  })
}

export function useStorageObjectsPaged(limit: number, offset: number, prefix = '') {
  const projectId = useProjectId()
  return useQuery({
    queryKey: storageKeys.objectsPaged(projectId, limit, offset, prefix),
    queryFn: () => api.listStorageObjectsPaged(projectId, limit, offset, prefix),
    enabled: !!projectId,
    placeholderData: (prev) => prev,
  })
}

export function useSaveStorageSettings() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (config: StorageConfig) => api.saveStorageSettings(config),
    onSuccess: () => qc.invalidateQueries({ queryKey: storageKeys.settings() }),
  })
}

export function useTestStorageSettings() {
  return useMutation({
    mutationFn: (config: StorageConfig) => api.testStorageSettings(config),
  })
}

export function useDeleteObject() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (key: string) => api.deleteStorageObject(projectId, key),
    onSuccess: () => qc.invalidateQueries({ queryKey: storageKeys.objectsAll(projectId) }),
  })
}

export function useUploadObject() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ file, key }: { file: File; key?: string }) =>
      api.uploadStorageObject(projectId, file, key),
    onSuccess: () => qc.invalidateQueries({ queryKey: storageKeys.objectsAll(projectId) }),
  })
}
