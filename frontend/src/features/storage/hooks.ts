// storage feature hooks — React Query wrappers
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import * as api from './api'
import type { StorageConfig } from './types'

export const storageKeys = {
  settings: () => ['storage', 'settings'] as const,
  objects: (prefix?: string) => ['storage', 'objects', prefix] as const,
}

export function useStorageSettings() {
  return useQuery({
    queryKey: storageKeys.settings(),
    queryFn: api.getStorageSettings,
  })
}

export function useStorageObjects(prefix = '') {
  return useQuery({
    queryKey: storageKeys.objects(prefix),
    queryFn: () => api.listStorageObjects(prefix),
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
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.deleteStorageObject,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['storage', 'objects'] }),
  })
}

export function useUploadObject() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ file, key }: { file: File; key?: string }) =>
      api.uploadStorageObject(file, key),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['storage', 'objects'] }),
  })
}
