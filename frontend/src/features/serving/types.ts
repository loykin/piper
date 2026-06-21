// serving feature types

export interface Service {
  name: string
  run_id: string
  artifact: string
  status: 'running' | 'stopped' | 'failed'
  endpoint: string
  namespace?: string
  pid: number
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
  endpoint: string
  namespace?: string
  pid: number
  yaml: string
  deployed_at: string
  stopped_at: string
}

export type ServingWorkerInfo = import('@/features/workers/types').Worker
