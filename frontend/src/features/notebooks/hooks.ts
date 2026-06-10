// notebooks feature hooks — React Query wrappers
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import * as api from './api'

export const notebookKeys = {
  all: ['notebooks'] as const,
  list: () => ['notebooks', 'list'] as const,
  one: (name: string) => ['notebooks', name] as const,
  volumes: () => ['notebook-volumes'] as const,
  volumeFiles: (volumeId: string) => ['notebook-volumes', volumeId, 'files'] as const,
  workers: () => ['notebook-workers'] as const,
}

export function useNotebooks() {
  return useQuery({
    queryKey: notebookKeys.list(),
    queryFn: api.listNotebooks,
    refetchInterval: 5000,
  })
}

export function useNotebook(name: string) {
  return useQuery({
    queryKey: notebookKeys.one(name),
    queryFn: () => api.getNotebook(name),
    refetchInterval: (query) => {
      const status = query.state.data?.status
      return status === 'running' || status === 'provisioning' || status === 'starting' || status === 'stopping'
        ? 3000 : 5000
    },
  })
}

export function useCreateNotebook() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ yaml, volumeId }: { yaml: string; volumeId?: string }) =>
      api.createNotebook(yaml, volumeId),
    onSuccess: () => qc.invalidateQueries({ queryKey: notebookKeys.all }),
  })
}

export function useStopNotebook() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.stopNotebook,
    onSuccess: () => qc.invalidateQueries({ queryKey: notebookKeys.all }),
  })
}

export function useStartNotebook() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.startNotebook,
    onSuccess: () => qc.invalidateQueries({ queryKey: notebookKeys.all }),
  })
}

export function useDeleteNotebook() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.deleteNotebook,
    onSuccess: () => qc.invalidateQueries({ queryKey: notebookKeys.all }),
  })
}

export function useNotebookVolumes() {
  return useQuery({
    queryKey: notebookKeys.volumes(),
    queryFn: api.listNotebookVolumes,
    refetchInterval: 5000,
  })
}

export function useVolumeFiles(volumeId: string, ext?: string) {
  return useQuery({
    queryKey: notebookKeys.volumeFiles(volumeId),
    queryFn: () => api.listVolumeFiles(volumeId, ext),
    enabled: !!volumeId,
    refetchInterval: (query) => {
      return query.state.data?.state === 'transitioning' ? 2000 : false
    },
  })
}

export function useNotebookWorkers() {
  return useQuery({
    queryKey: notebookKeys.workers(),
    queryFn: api.listNotebookWorkers,
    refetchInterval: 10000,
  })
}

export function usePurgeVolume() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.purgeNotebookVolume,
    onSuccess: () => qc.invalidateQueries({ queryKey: notebookKeys.volumes() }),
  })
}
