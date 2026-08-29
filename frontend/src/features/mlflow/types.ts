export interface MLflowIntegration {
  id: string
  project_id: string
  name: string
  tracking_uri: string
  credential_ref: string
  enabled: boolean
  default: boolean
  export_pipelines: boolean
  export_notebook_executions: boolean
  experiment_template: string
  artifact_mode: 'reference'
  created_by?: string
  created_at: string
  updated_at: string
}

export interface MLflowIntegrationDetail extends MLflowIntegration {
  system_enabled: boolean
  health: 'healthy' | 'degraded' | 'disabled'
  pending_events: number
  dead_events: number
  oldest_pending_age_seconds?: number
}

export type MLflowIntegrationRequest = Pick<MLflowIntegration,
  'name' | 'tracking_uri' | 'credential_ref' | 'enabled' | 'default' |
  'export_pipelines' | 'export_notebook_executions' | 'experiment_template' | 'artifact_mode'>

export interface MLflowTestResult { ok: boolean; message: string }

export interface MLflowRunLink {
  integration_id: string
  mlflow_run_id?: string
  mlflow_run_url?: string
  sync_status: string
  last_error_code?: string
  last_error_message?: string
  last_synced_at?: string
}
