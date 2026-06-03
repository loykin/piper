const BASE = ''

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

async function requestJSON<T>(input: RequestInfo | URL, init?: RequestInit): Promise<T> {
  const res = await fetch(input, {
    headers: { 'Content-Type': 'application/json', ...(init?.headers ?? {}) },
    ...init,
  })
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    throw new Error(body || `${res.status} ${res.statusText}`)
  }
  return res.json()
}

export async function getStorageSettings(): Promise<StorageSettingsView> {
  return requestJSON(`${BASE}/api/storage/settings`)
}

export async function saveStorageSettings(config: StorageConfig): Promise<StorageSettingsView> {
  return requestJSON(`${BASE}/api/storage/settings`, {
    method: 'PUT',
    body: JSON.stringify(config),
  })
}

export async function listStorageObjects(prefix = ''): Promise<StorageObjectInfo[]> {
  const url = new URL(`${BASE}/api/storage/objects`, window.location.origin)
  if (prefix) url.searchParams.set('prefix', prefix)
  return requestJSON(url)
}

export function storageObjectURL(key: string): string {
  const url = new URL(`${BASE}/api/storage/object`, window.location.origin)
  url.searchParams.set('key', key)
  return url.toString()
}

export async function deleteStorageObject(key: string): Promise<void> {
  const url = new URL(`${BASE}/api/storage/object`, window.location.origin)
  url.searchParams.set('key', key)
  const res = await fetch(url, { method: 'DELETE' })
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    throw new Error(body || `${res.status} ${res.statusText}`)
  }
}

export async function uploadStorageObject(file: File, key?: string): Promise<StorageUploadResult> {
  const form = new FormData()
  form.set('file', file)
  if (key) form.set('key', key)
  const res = await fetch(`${BASE}/api/storage/object`, {
    method: 'POST',
    body: form,
  })
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    throw new Error(body || `${res.status} ${res.statusText}`)
  }
  return res.json()
}
