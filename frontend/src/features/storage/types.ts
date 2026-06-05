// storage feature types

export interface StorageConfig {
  url: string
  disabled: boolean
  token: string
}

export interface StorageSettingsView {
  config_path: string
  config: StorageConfig
  effective: {
    status: 'enabled' | 'disabled' | 'unavailable'
    backend?: string
    reason?: string
  }
  restart_required: boolean
}

export interface StorageObjectInfo {
  key: string
  size: number
  modified_at: string
  download_url: string
}

export interface StorageUploadResult {
  key: string
}
