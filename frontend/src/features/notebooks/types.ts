// notebooks feature types

export interface NotebookServer {
  name: string
  status: 'provisioning' | 'starting' | 'running' | 'stopping' | 'stopped' | 'failed'
  env: string
  image: string
  endpoint: string
  pid: number
  work_dir: string
  token: string
  runtime_id?: string
  volume_id: string
  yaml: string
  created_at: string
  updated_at: string
}

export interface NotebookHistory {
  id: number
  name: string
  status: string
  env: string
  endpoint: string
  pid: number
  work_dir: string
  runtime_id?: string
  volume_id: string
  image: string
  yaml: string
  deployed_at: string
  stopped_at: string
}

export interface NotebookVolume {
  id: string
  label: string
  work_dir: string
  status: 'bound' | 'released'
  runtime_id: string
  created_at: string
  updated_at: string
}
