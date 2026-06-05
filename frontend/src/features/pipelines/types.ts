// pipelines feature types

export interface PipelineTemplate {
  id: string
  name: string
  yaml: string
  snapshot_id: string
  volume_id: string
  created_at: string
}

export interface SubmitPipelineRequest {
  name: string
  yaml: string
  volume_id?: string
}

export interface TriggerRunRequest {
  params?: Record<string, unknown>
}

export interface DeployRequest {
  cron: string
  enabled?: boolean
  params?: Record<string, unknown>
}
