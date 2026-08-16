export type {
  StorageConfig, StorageSettingsView, StorageObjectInfo, StorageUploadResult, StorageTestResult,
} from './types'

import type { StorageConfig, StorageSettingsView, StorageObjectInfo, StorageUploadResult, StorageTestResult } from './types'
import { api, projectApi } from '@/lib/api'

// ── System-scoped (admin) ─────────────────────────────────────────────────────

export async function getStorageSettings(): Promise<StorageSettingsView> {
  return api.get<StorageSettingsView>('/api/storage/settings')
}

export async function saveStorageSettings(config: StorageConfig): Promise<StorageSettingsView> {
  return api.put<StorageSettingsView>('/api/storage/settings', config)
}

export async function testStorageSettings(config: StorageConfig): Promise<StorageTestResult> {
  return api.post<StorageTestResult>('/api/storage/settings/test', config)
}

// ── Project-scoped ────────────────────────────────────────────────────────────

/** Paginated object listing — see `listNotebookVolumesPaged` for the shared shape. */
export async function listStorageObjectsPaged(
  projectId: string,
  limit: number,
  offset: number,
  prefix = '',
): Promise<{ objects: StorageObjectInfo[]; total: number }> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  if (prefix) params.set('prefix', prefix)
  const { data, total } = await projectApi(projectId).getWithTotal<StorageObjectInfo[]>(`/storage/objects?${params.toString()}`)
  return { objects: Array.isArray(data) ? data : [], total: total ?? 0 }
}

export function storageObjectURL(projectId: string, key: string): string {
  const base = `/api/projects/${encodeURIComponent(projectId)}/storage/object`
  return `${base}?key=${encodeURIComponent(key)}`
}

export async function deleteStorageObject(projectId: string, key: string): Promise<void> {
  return projectApi(projectId).delete(`/storage/object?key=${encodeURIComponent(key)}`)
}

export async function uploadStorageObject(
  projectId: string,
  file: File,
  key?: string,
): Promise<StorageUploadResult> {
  const form = new FormData()
  form.set('file', file)
  if (key) form.set('key', key)
  return projectApi(projectId).upload<StorageUploadResult>('/storage/object', form)
}
