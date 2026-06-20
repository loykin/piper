import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useProjectId } from '@/lib/projectContext'
import { openViewer, stopViewer } from './api'
import type { OpenViewerRequest } from './types'

export const viewerKeys = {
  all: (projectId: string) => ['viewers', projectId] as const,
  list: (projectId: string) => ['viewers', projectId, 'list'] as const,
  one: (projectId: string, id: string) => ['viewers', projectId, id] as const,
}

export function useOpenViewer(runId: string, step: string, artifact: string) {
  const projectId = useProjectId()
  return useMutation({
    mutationFn: (req: OpenViewerRequest) =>
      openViewer(projectId, runId, step, artifact, req),
    onSuccess: (viewer) => {
      window.open(viewer.url, '_blank', 'noopener,noreferrer')
    },
  })
}

export function useStopViewer() {
  const projectId = useProjectId()
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => stopViewer(projectId, id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: viewerKeys.all(projectId) })
    },
  })
}
