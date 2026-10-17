export interface SystemSettings {
  artifact_store: {
    status: 'enabled' | 'disabled' | 'unavailable'
    backend?: string
    reason?: string
  }
  runtime: {
    type: '' | 'baremetal' | 'docker' | 'k8s'
    /** k8s only: runtime.namespaces, the namespaces workloads may use. */
    namespaces?: string[]
  }
}
