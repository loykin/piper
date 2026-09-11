export type NotebookExecutionStatus =
  | 'awaiting_approval' | 'queued' | 'running' | 'cancelling'
  | 'succeeded' | 'failed' | 'timed_out' | 'cancelled' | 'conflicted'

export interface NotebookExecution {
  id: string
  project_id: string
  notebook_name: string
  notebook_path: string
  result_path?: string
  kernel_session_id?: string
  kind: 'notebook' | 'cell'
  status: NotebookExecutionStatus
  requested_by?: string
  // Server-resolved usernames (pkg/notebook/execution/handler.go's
  // attachActorNames) — the raw *_by IDs above may belong to a system admin
  // with no membership row in this project, which a non-admin viewer has no
  // other way to resolve since GET /users is system-admin-gated.
  requested_by_username?: string
  client_id?: string
  source_sha256?: string
  current_cell: number
  total_cells: number
  error_code?: string
  error_message?: string
  output_summary?: string
  approved_by?: string
  approved_by_username?: string
  approved_at?: string
  denied_by?: string
  denied_by_username?: string
  denied_at?: string
  queued_at: string
  started_at?: string
  finished_at?: string
  updated_at: string
}

export type ExecutionPolicy = 'disabled' | 'approval_required' | 'allowed'

export interface ExecutionPolicyResponse {
  mcp_policy: ExecutionPolicy
}
