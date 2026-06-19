import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useProjectId } from '@/lib/projectContext'
import { openViewer, stopViewer } from './api'
import type { OpenViewerRequest } from './types'

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
      void queryClient.invalidateQueries({ queryKey: ['viewers', projectId] })
    },
  })
}
