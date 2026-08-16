// storage feature types

export interface StorageConfig {
  url: string
  disabled: boolean
  token: string
  credentialRef?: string
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

export interface StorageTestResult {
  ok: boolean
  message: string
}

export interface StorageObjectInfo {
  key: string
  size: number
  modified_at: string
  download_url: string
  // is_dir marks a pseudo-directory entry (a common prefix one level below
  // the queried prefix) rather than an actual uploaded object. Other fields
  // are zero-valued for directory entries — see storage.Store.List's
  // delimiter semantics on the Go side.
  is_dir: boolean
}

export interface StorageUploadResult {
  key: string
}
