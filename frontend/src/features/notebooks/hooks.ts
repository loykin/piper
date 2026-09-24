// notebooks feature hooks — React Query wrappers
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import * as api from './api'
import { useProjectId } from '@/features/projects/context'
import { backgroundPolling, backgroundPollingNotifications } from '@/lib/query'

export const notebookKeys = {
  all: (projectId: string) => ['notebooks', projectId] as const,
  list: (projectId: string) => ['notebooks', projectId, 'list'] as const,
  listPaged: (projectId: string, limit: number, offset: number) =>
    ['notebooks', projectId, 'list', 'paged', limit, offset] as const,
  one: (projectId: string, name: string) => ['notebooks', projectId, name] as const,
  historyPaged: (projectId: string, limit: number, offset: number) =>
    ['notebooks', projectId, 'history', limit, offset] as const,
  volumes: (projectId: string) => ['notebook-volumes', projectId] as const,
  volumesPaged: (projectId: string, limit: number, offset: number) =>
    ['notebook-volumes', projectId, 'paged', limit, offset] as const,
  volumeFiles: (projectId: string, volumeId: string) =>
    ['notebook-volumes', projectId, volumeId, 'files'] as const,
}

export function useNotebooks() {
  const projectId = useProjectId()
  return useQuery({
    queryKey: notebookKeys.list(projectId),
    queryFn: () => api.listNotebooks(projectId),
    enabled: !!projectId,
    ...backgroundPolling(5000),
  })
}

export function useNotebooksPaged(limit: number, offset: number) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: notebookKeys.listPaged(projectId, limit, offset),
    queryFn: () => api.listNotebooksPaged(projectId, limit, offset),
    enabled: !!projectId,
    placeholderData: (prev) => prev,
    ...backgroundPolling(5000),
  })
}

export function useNotebook(name: string) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: notebookKeys.one(projectId, name),
    queryFn: () => api.getNotebook(projectId, name),
    enabled: !!projectId && !!name,
    refetchInterval: (query) => {
      const status = query.state.data?.status
      return status === 'running' || status === 'provisioning' || status === 'starting' || status === 'stopping'
        ? 3000 : 5000
    },
    ...backgroundPollingNotifications,
  })
}

/** Like `useNotebooks`, but for a `limit`-paginated history page — also returns `total`. */
export function useNotebookHistoryPaged(limit: number, offset: number) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: notebookKeys.historyPaged(projectId, limit, offset),
    queryFn: () => api.listNotebookHistoryPaged(projectId, limit, offset),
    enabled: !!projectId,
    placeholderData: (prev) => prev,
  })
}

export function useCreateNotebook() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ yaml, volumeId }: { yaml: string; volumeId?: string }) =>
      api.createNotebook(projectId, yaml, volumeId),
    onSuccess: () => qc.invalidateQueries({ queryKey: notebookKeys.all(projectId) }),
  })
}

export function useStopNotebook() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => api.stopNotebook(projectId, name),
    onSuccess: () => qc.invalidateQueries({ queryKey: notebookKeys.all(projectId) }),
  })
}

export function useStartNotebook() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => api.startNotebook(projectId, name),
    onSuccess: () => qc.invalidateQueries({ queryKey: notebookKeys.all(projectId) }),
  })
}

export function useDeleteNotebook() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => api.deleteNotebook(projectId, name),
    onSuccess: () => qc.invalidateQueries({ queryKey: notebookKeys.all(projectId) }),
  })
}

export function useNotebookVolumes() {
  const projectId = useProjectId()
  return useQuery({
    queryKey: notebookKeys.volumes(projectId),
    queryFn: () => api.listNotebookVolumes(projectId),
    enabled: !!projectId,
    ...backgroundPolling(5000),
  })
}

/** Like `useNotebookVolumes`, but for a `limit`-paginated page — also returns `total`. */
export function useNotebookVolumesPaged(limit: number, offset: number) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: notebookKeys.volumesPaged(projectId, limit, offset),
    queryFn: () => api.listNotebookVolumesPaged(projectId, limit, offset),
    enabled: !!projectId,
    placeholderData: (prev) => prev,
    ...backgroundPolling(5000),
  })
}

export function useVolumeFiles(volumeId: string, ext?: string) {
  const projectId = useProjectId()
  const qc = useQueryClient()
  const queryKey = notebookKeys.volumeFiles(projectId, volumeId)
  return useQuery({
    queryKey,
    queryFn: async () => {
      const result = await api.listVolumeFiles(projectId, volumeId, ext)
      // A transitioning volume (notebook starting/stopping) answers with no
      // files; keep showing the last known list instead of flashing empty.
      if (result.state === 'transitioning') {
        const previous = qc.getQueryData<typeof result>(queryKey)
        return { ...result, files: previous?.files ?? [] }
      }
      return result
    },
    enabled: !!projectId && !!volumeId,
    refetchInterval: (query) =>
      query.state.data?.state === 'transitioning' ? 2000 : false,
    ...backgroundPollingNotifications,
  })
}

export function usePurgeVolume() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.purgeNotebookVolume(projectId, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: notebookKeys.volumes(projectId) }),
  })
}
