export type ViewerStatus = 'starting' | 'running' | 'failed' | 'stopped'

export interface Viewer {
  id: string
  project_id: string
  type: string
  run_id: string
  step_name: string
  artifact: string
  status: ViewerStatus
  endpoint?: string
  created_at: string
  updated_at: string
  expires_at?: string
  url: string
}

export interface OpenViewerRequest {
  type: string
}
