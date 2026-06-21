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
  worker_id?: string
  volume_id: string
  yaml: string
  created_at: string
  updated_at: string
}

export interface NotebookVolume {
  id: string
  label: string
  work_dir: string
  status: 'bound' | 'released'
  worker_id: string
  created_at: string
  updated_at: string
}

export type NotebookWorkerInfo = import('@/features/workers/types').Worker
