import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useProjectId } from '@/lib/projectContext'
import {
  approveNotebookExecution, cancelNotebookExecution, denyNotebookExecution,
  getExecutionPolicy, listNotebookExecutions, updateExecutionPolicy,
} from './api'
import type { ExecutionPolicy, NotebookExecution } from './types'

export const notebookExecutionKeys = {
  all: (projectId: string) => ['notebook-executions', projectId] as const,
  list: (projectId: string, limit: number, offset: number, notebook?: string) => ['notebook-executions', projectId, limit, offset, notebook ?? 'all'] as const,
  policy: (projectId: string) => ['notebook-executions', projectId, 'policy'] as const,
}

export function useNotebookExecutions(limit: number, offset: number, notebook?: string) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: notebookExecutionKeys.list(projectId, limit, offset, notebook),
    queryFn: () => listNotebookExecutions(projectId, limit, offset, notebook),
    enabled: !!projectId,
    placeholderData: previous => previous,
    refetchInterval: query => query.state.data?.executions.some(item => ['queued', 'running', 'cancelling'].includes(item.status)) ? 2000 : false,
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
