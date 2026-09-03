import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { backgroundPollingNotifications } from '@/lib/query'
import { useProjectId } from '@/lib/projectContext'
import {
  approveNotebookExecution, cancelNotebookExecution, denyNotebookExecution,
  getExecutionPolicy, getNotebookExecution, listNotebookExecutions, updateExecutionPolicy,
} from './api'
import type { ExecutionPolicy, NotebookExecution, NotebookExecutionStatus } from './types'

export const notebookExecutionKeys = {
  all: (projectId: string) => ['notebook-executions', projectId] as const,
  list: (projectId: string, limit: number, offset: number, notebook?: string) => ['notebook-executions', projectId, limit, offset, notebook ?? 'all'] as const,
  one: (projectId: string, id: string) => ['notebook-executions', projectId, 'one', id] as const,
  policy: (projectId: string) => ['notebook-executions', projectId, 'policy'] as const,
}

const TERMINAL_STATUSES: NotebookExecutionStatus[] = ['succeeded', 'failed', 'timed_out', 'cancelled', 'conflicted']

/**
 * Live single-execution query, seeded with the row snapshot the detail panel
 * was opened with (`initial`) so the panel renders immediately, then kept
 * current by polling while the execution hasn't reached a terminal status.
 * Approve/deny/cancel mutations below invalidate `notebookExecutionKeys.all`,
 * which is a prefix of this query's key too, so an admin's own approval
 * refetches this panel immediately instead of waiting for the next poll tick.
 */
export function useExecution(notebookName: string, id: string, initial?: NotebookExecution) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: notebookExecutionKeys.one(projectId, id),
    queryFn: () => getNotebookExecution(projectId, notebookName, id),
    enabled: !!projectId && !!notebookName && !!id,
    initialData: initial,
    refetchInterval: query => (query.state.data && TERMINAL_STATUSES.includes(query.state.data.status) ? false : 2000),
    ...backgroundPollingNotifications,
  })
}

export function useNotebookExecutions(limit: number, offset: number, notebook?: string) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: notebookExecutionKeys.list(projectId, limit, offset, notebook),
    queryFn: () => listNotebookExecutions(projectId, limit, offset, notebook),
    enabled: !!projectId,
    placeholderData: previous => previous,
    refetchInterval: query => query.state.data?.executions.some(item => ['queued', 'running', 'cancelling'].includes(item.status)) ? 2000 : false,
    ...backgroundPollingNotifications,
  })
}

export function useExecutionPolicy() {
  const projectId = useProjectId()
  return useQuery({ queryKey: notebookExecutionKeys.policy(projectId), queryFn: () => getExecutionPolicy(projectId), enabled: !!projectId })
}

function useExecutionAction(action: (projectId: string, execution: NotebookExecution) => Promise<void>) {
  const projectId = useProjectId()
  const client = useQueryClient()
  return useMutation({
    mutationFn: (execution: NotebookExecution) => action(projectId, execution),
    onSuccess: () => client.invalidateQueries({ queryKey: notebookExecutionKeys.all(projectId) }),
  })
}

export function useApproveExecution() { return useExecutionAction(approveNotebookExecution) }
export function useDenyExecution() { return useExecutionAction(denyNotebookExecution) }
export function useCancelExecution() { return useExecutionAction(cancelNotebookExecution) }

export function useUpdateExecutionPolicy() {
  const projectId = useProjectId()
  const client = useQueryClient()
  return useMutation({
    mutationFn: (policy: ExecutionPolicy) => updateExecutionPolicy(projectId, policy),
    onSuccess: () => client.invalidateQueries({ queryKey: notebookExecutionKeys.policy(projectId) }),
  })
}
