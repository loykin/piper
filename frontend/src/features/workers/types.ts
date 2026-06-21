// Connection metadata returned by GET /api/workers.
export interface Worker {
  id: string
  infrastructure: 'baremetal' | 'docker' | 'k8s'
  hostname?: string
  capabilities: Array<'pipeline' | 'notebook' | 'serving'>
  cluster_name?: string
  namespaces?: string[]
  labels?: Record<string, string>
  capacity?: number
  registered_at: string
  last_seen: string
}

export interface WorkerPodPolicy {
  worker_id: string
  pod_template: Record<string, unknown>
  updated_at: string
  updated_by: string
}
