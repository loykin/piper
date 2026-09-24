import { projectApi } from '@/lib/api'
import type { ExecutionPolicy, ExecutionPolicyResponse, NotebookExecution } from './types'

export async function listNotebookExecutions(projectId: string, limit: number, offset: number, notebook?: string) {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  if (notebook) params.set('notebook', notebook)
  return projectApi(projectId).getPaged<NotebookExecution>(`/notebook-executions?${params}`)
}

export function getNotebookExecution(projectId: string, id: string) {
  return projectApi(projectId).get<NotebookExecution>(`/notebook-executions/${encodeURIComponent(id)}`)
}

export function getExecutionPolicy(projectId: string) {
  return projectApi(projectId).get<ExecutionPolicyResponse>('/notebook-execution-policy')
}

export function updateExecutionPolicy(projectId: string, policy: ExecutionPolicy) {
  return projectApi(projectId).put<void>('/notebook-execution-policy', { mcp_policy: policy })
}

function executionPath(execution: NotebookExecution, action: string) {
  return `/notebook-executions/${encodeURIComponent(execution.id)}/${encodeURIComponent(action)}`
}

export function approveNotebookExecution(projectId: string, execution: NotebookExecution) {
  return projectApi(projectId).post<void>(executionPath(execution, 'approve'))
}

export function denyNotebookExecution(projectId: string, execution: NotebookExecution) {
  return projectApi(projectId).post<void>(executionPath(execution, 'deny'))
}

export function cancelNotebookExecution(projectId: string, execution: NotebookExecution) {
  return projectApi(projectId).post<void>(executionPath(execution, 'cancel'))
}
