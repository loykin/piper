// runs feature hooks — React Query wrappers
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import * as api from './api'
import type { LogLine, RunFilter } from './types'

export const runKeys = {
  all: ['runs'] as const,
  list: (filter?: RunFilter) => ['runs', 'list', filter] as const,
  one: (id: string) => ['runs', id] as const,
  steps: (id: string) => ['runs', id, 'steps'] as const,
  artifacts: (runId: string, stepId: string) => ['runs', runId, 'artifacts', stepId] as const,
}

export function useRuns(filter?: RunFilter) {
  return useQuery({
    queryKey: runKeys.list(filter),
    queryFn: () => api.listRuns(filter),
    refetchInterval: 5000,
  })
}

export function useRun(id: string) {
  return useQuery({
    queryKey: runKeys.one(id),
    queryFn: () => api.getRun(id),
    refetchInterval: (query) =>
      query.state.data?.run?.status === 'running' ? 2000 : 5000,
  })
}

export function useRunSteps(runId: string) {
  return useQuery({
    queryKey: runKeys.steps(runId),
    queryFn: async () => {
      const { steps } = await api.getRun(runId)
      return steps
    },
    refetchInterval: (query) => {
      const steps = query.state.data ?? []
      return steps.some(s => s.status === 'running' || s.status === 'pending') ? 2000 : 5000
    },
  })
}

export function useStepArtifacts(runId: string, stepId: string | null) {
  return useQuery({
    queryKey: runKeys.artifacts(runId, stepId ?? ''),
    queryFn: () => api.listArtifacts(runId),
    enabled: !!stepId,
  })
}

export function useDeleteRun() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.deleteRun,
    onSuccess: () => qc.invalidateQueries({ queryKey: runKeys.all }),
  })
}

export function useCancelRun() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.cancelRun,
    onSuccess: (_data, runId) =>
      qc.invalidateQueries({ queryKey: runKeys.one(runId) }),
  })
}

export function useRerunRun() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, failedOnly }: { id: string; failedOnly?: boolean }) =>
      api.rerunRun(id, failedOnly),
    onSuccess: () => qc.invalidateQueries({ queryKey: runKeys.all }),
  })
}

export function useRetryStep() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ runId, stepId }: { runId: string; stepId: string }) =>
      api.retryStep(runId, stepId),
    onSuccess: (_data, { runId }) =>
      qc.invalidateQueries({ queryKey: runKeys.one(runId) }),
  })
}

// EventSource hook for streaming logs
export function useRunLogs(runId: string, stepId: string | null) {
  const [lines, setLines] = useState<LogLine[]>([])
  const [done, setDone] = useState(false)

  useEffect(() => {
    if (!stepId) { setLines([]); setDone(false); return }
    setLines([]); setDone(false)
    const es = api.streamLogs(
      runId,
      stepId,
      (line) => setLines(prev => [...prev, line]),
      () => setDone(true),
    )
    return () => es.close()
  }, [runId, stepId])

  return { lines, done }
}
