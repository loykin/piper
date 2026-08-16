import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import * as api from './api'
import type { StorageConfig } from './types'
import { useProjectId } from '@/lib/projectContext'
import { ApiError } from '@/lib/api'

export const storageKeys = {
  settings: () => ['storage', 'settings'] as const,
  objects: (projectId: string, prefix?: string) => ['storage', projectId, 'objects', prefix] as const,
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

export function useStorageObjects(prefix = '') {
  const projectId = useProjectId()
  return useQuery({
    queryKey: storageKeys.objects(projectId, prefix),
    queryFn: () => api.listStorageObjects(projectId, prefix),
    enabled: !!projectId,
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
    onSuccess: () => qc.invalidateQueries({ queryKey: storageKeys.objects(projectId) }),
  })
}

export function useUploadObject() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ file, key }: { file: File; key?: string }) =>
      api.uploadStorageObject(projectId, file, key),
    onSuccess: () => qc.invalidateQueries({ queryKey: storageKeys.objects(projectId) }),
  })
}
