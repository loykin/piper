// pipelines feature types

export interface PipelineTemplate {
  id: string
  name: string
  version: number
  description: string
  tags: string[]
  yaml: string
  snapshot_id: string
  volume_id: string
  created_at: string
  updated_at: string
}

export interface CreatePipelineRequest {
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

