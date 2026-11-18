// serving feature types

export interface Service {
  name: string
  run_id: string
  artifact: string
  status: 'running' | 'stopped' | 'failed'
  /** Why the service reached its current status; empty while healthy. */
  reason: string
  endpoint: string
  namespace?: string
  pid: number
  runtime_id?: string
  yaml: string
  created_at: string
  updated_at: string
}

export interface ServiceHistory {
  id: number
  name: string
  run_id: string
  artifact: string
  status: string
  reason: string
  endpoint: string
  namespace?: string
  pid: number
  yaml: string
  deployed_at: string
  stopped_at: string
}
