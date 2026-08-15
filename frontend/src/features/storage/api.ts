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

export async function listStorageObjects(
  projectId: string,
  prefix = '',
): Promise<StorageObjectInfo[]> {
  const qs = prefix ? `?prefix=${encodeURIComponent(prefix)}` : ''
  const data = await projectApi(projectId).get<StorageObjectInfo[]>(`/storage/objects${qs}`)
  return Array.isArray(data) ? data : []
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
