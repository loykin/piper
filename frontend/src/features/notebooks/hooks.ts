// notebooks feature hooks — React Query wrappers
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import * as api from './api'
import { api as sysApi } from '@/lib/api'
import { useProjectId } from '@/lib/projectContext'
import type { NotebookWorkerInfo } from './types'

export const notebookKeys = {
  all: (projectId: string) => ['notebooks', projectId] as const,
  list: (projectId: string) => ['notebooks', projectId, 'list'] as const,
  one: (projectId: string, name: string) => ['notebooks', projectId, name] as const,
  volumes: (projectId: string) => ['notebook-volumes', projectId] as const,
  volumeFiles: (projectId: string, volumeId: string) =>
    ['notebook-volumes', projectId, volumeId, 'files'] as const,
}

export function useNotebooks() {
  const projectId = useProjectId()
  return useQuery({
    queryKey: notebookKeys.list(projectId),
    queryFn: () => api.listNotebooks(projectId),
    enabled: !!projectId,
    refetchInterval: 5000,
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
    refetchInterval: 5000,
  })
}

export function useVolumeFiles(volumeId: string, ext?: string) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: notebookKeys.volumeFiles(projectId, volumeId),
    queryFn: () => api.listVolumeFiles(projectId, volumeId, ext),
    enabled: !!projectId && !!volumeId,
    refetchInterval: (query) =>
      query.state.data?.state === 'transitioning' ? 2000 : false,
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


export function useNotebookWorkers() {
  return useQuery({
    queryKey: ['notebook-workers'],
    queryFn: () => sysApi.get<NotebookWorkerInfo[]>('/api/notebook-workers').then(d => Array.isArray(d) ? d : []),
    refetchInterval: 10000,
  })
}
