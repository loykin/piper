// runs feature hooks — React Query wrappers
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import * as api from './api'
import type { LogLine, RunFilter, SweepRequest } from './types'
import { useProjectId } from '@/lib/projectContext'
import { backgroundPolling, backgroundPollingNotifications } from '@/lib/query'

export const runKeys = {
  all: (projectId: string) => ['runs', projectId] as const,
  list: (projectId: string, filter?: RunFilter) => ['runs', projectId, 'list', filter] as const,
  one: (projectId: string, id: string) => ['runs', projectId, id] as const,
  steps: (projectId: string, id: string) => ['runs', projectId, id, 'steps'] as const,
  metrics: (projectId: string, id: string) => ['runs', projectId, id, 'metrics'] as const,
  artifacts: (projectId: string, runId: string) => ['runs', projectId, runId, 'artifacts'] as const,
  statsCapabilities: (projectId: string) => ['runs', projectId, 'stats-capabilities'] as const,
}

export function useStatsCapabilities() {
  const projectId = useProjectId()
  return useQuery({
    queryKey: runKeys.statsCapabilities(projectId),
    queryFn: () => api.getStatsCapabilities(projectId),
    enabled: !!projectId,
    ...backgroundPolling(5000),
  })
}

export function useRuns(filter?: RunFilter) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: runKeys.list(projectId, filter),
    queryFn: () => api.listRuns(projectId, filter),
    enabled: !!projectId,
    ...backgroundPolling(5000),
  })
}

/** Like `useRuns`, but for a `filter.limit`-paginated page — also returns `total`. */
export function useRunsPaged(filter: RunFilter) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: runKeys.list(projectId, filter),
    queryFn: () => api.listRunsPaged(projectId, filter),
    enabled: !!projectId,
    placeholderData: (prev) => prev,
    ...backgroundPolling(5000),
  })
}

export function useRun(id: string) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: runKeys.one(projectId, id),
    queryFn: () => api.getRun(projectId, id),
    enabled: !!projectId && !!id,
    ...backgroundPolling(5000),
  })
}

export function useRunSteps(runId: string) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: runKeys.steps(projectId, runId),
    queryFn: () => api.getRunSteps(projectId, runId),
    enabled: !!projectId && !!runId,
    refetchInterval: (query) => {
      const steps = query.state.data ?? []
      return steps.some(s => s.status === 'running' || s.status === 'pending') ? 2000 : 5000
    },
    ...backgroundPollingNotifications,
  })
}

export function useStepArtifacts(runId: string, stepId: string | null) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: runKeys.artifacts(projectId, runId),
    queryFn: () => api.listArtifacts(projectId, runId),
    enabled: !!projectId && !!stepId,
  })
}

export function useRunMetrics(runId: string) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: runKeys.metrics(projectId, runId),
    queryFn: () => api.getRunMetrics(projectId, runId),
    enabled: !!projectId && !!runId,
  })
}

export function useDeleteRun() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.deleteRun(projectId, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: runKeys.all(projectId) }),
  })
}

export function useCancelRun() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.cancelRun(projectId, id),
    onSuccess: (_data, id) => qc.invalidateQueries({ queryKey: runKeys.one(projectId, id) }),
  })
}

export function useRerunRun() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.rerunRun(projectId, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: runKeys.all(projectId) }),
  })
}

export function useCreateSweep() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (req: SweepRequest) => api.createSweep(projectId, req),
    onSuccess: () => qc.invalidateQueries({ queryKey: runKeys.all(projectId) }),
  })
}

export function useRetryStep() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ runId, stepId }: { runId: string; stepId: string }) =>
      api.retryStep(projectId, runId, stepId),
    onSuccess: (_data, { runId }) =>
      qc.invalidateQueries({ queryKey: runKeys.one(projectId, runId) }),
  })
}

export function useCreateRun() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ yaml, params }: { yaml: string; params?: Record<string, unknown> }) =>
      api.createRun(projectId, yaml, params),
    onSuccess: () => qc.invalidateQueries({ queryKey: runKeys.all(projectId) }),
  })
}

// Streaming log hook using SSE
export function useRunLogs(runId: string, stepId: string | null) {
  const projectId = useProjectId()
  const [lines, setLines] = useState<LogLine[]>([])
  const [done, setDone] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    setLines([])
    setDone(false)
    setError(null)
    if (!stepId || !projectId) return

    let closed = false
    let cursor = ''
    let es: EventSource | null = null
    let retry: ReturnType<typeof setTimeout> | null = null
    const connect = () => {
      if (closed) return
      es = new EventSource(api.runLogsStreamURL(projectId, runId, stepId, cursor))
      es.onopen = () => setError(null)
      es.onmessage = (ev) => {
        if (ev.lastEventId) cursor = ev.lastEventId
        try {
          const line = JSON.parse(ev.data as string) as LogLine
          setLines(prev => [...prev, line])
        } catch { /* ignore */ }
      }
      es.addEventListener('done', () => { setDone(true); es?.close() })
      es.addEventListener('stats_error', (event) => {
        try {
          const payload = JSON.parse((event as MessageEvent<string>).data) as { message?: string }
          setError(payload.message ?? 'The statistics backend is unavailable.')
        } catch {
          setError('The statistics backend is unavailable.')
        }
        es?.close()
        retry = setTimeout(connect, 2000)
      })
      es.onerror = () => setError(prev => prev ?? 'The log stream was disconnected.')
    }
    connect()
    return () => { closed = true; es?.close(); if (retry) clearTimeout(retry) }
  }, [projectId, runId, stepId])

  return { lines, done, error }
}
