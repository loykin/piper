export interface SystemSettings {
  artifact_store: {
    status: 'enabled' | 'disabled' | 'unavailable'
    backend?: string
    reason?: string
  }
  runtime: {
    type: '' | 'baremetal' | 'docker' | 'k8s'
  }
}
