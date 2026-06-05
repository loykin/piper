// runs feature types

export interface Run {
  id: string
  schedule_id?: string
  owner_id?: string
  pipeline_name: string
  status: 'scheduled' | 'running' | 'success' | 'failed' | 'canceled'
  started_at: string
  ended_at?: string
  scheduled_at?: string
  pipeline_yaml: string
  steps?: Step[]
}

export interface Step {
  run_id: string
  step_name: string
  status: 'pending' | 'running' | 'done' | 'failed' | 'skipped' | 'canceled'
  started_at?: string
  ended_at?: string
  attempts: number
  error?: string
}

export interface LogLine {
  id: number
  run_id: string
  step_name: string
  ts: string
  stream: 'stdout' | 'stderr'
  line: string
}

export interface CreateRunOptions {
  params?: Record<string, unknown>
  owner_id?: string
  vars?: {
    scheduled_at?: string
  }
}

export interface ArtifactFile {
  path: string
  size: number
  modified_at: string
}

export interface ArtifactEntry {
  name: string
  files: ArtifactFile[]
}

export interface StepArtifacts {
  step: string
  artifacts: ArtifactEntry[]
}

export interface RunFilter {
  status?: string
  pipeline?: string
}
