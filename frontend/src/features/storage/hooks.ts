// storage feature hooks — React Query wrappers
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import * as api from './api'
import type { StorageConfig } from './types'
import { useProjectId } from '@/lib/projectContext'

export const storageKeys = {
  settings: () => ['storage', 'settings'] as const,
  objects: (projectId: string, prefix?: string) => ['storage', projectId, 'objects', prefix] as const,
}

export function useStorageSettings() {
  return useQuery({
    queryKey: storageKeys.settings(),
    queryFn: api.getStorageSettings,
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

export function useDeleteObject() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (key: string) => api.deleteStorageObject(projectId, key),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['storage', projectId, 'objects'] }),
  })
}

export function useUploadObject() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ file, key }: { file: File; key?: string }) =>
      api.uploadStorageObject(projectId, file, key),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['storage', projectId, 'objects'] }),
  })
}
