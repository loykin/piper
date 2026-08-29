export type {
  StorageConfig, StorageSettingsView, StorageObjectInfo, StorageUploadResult,
} from './types'

import type { StorageSettingsView, StorageObjectInfo, StorageUploadResult } from './types'
import { api, projectApi } from '@/lib/api'

// ── System-scoped (admin) ─────────────────────────────────────────────────────

// Read-only diagnostic: the effective config plus what's pending on disk.
// There is deliberately no save/update call here — the artifact storage
// backend (bucket/endpoint/region/which-backend) is deploy-time-only
// configuration, edited directly in storage.yaml and applied by restarting
// the server, the same as runtime.type or the database driver. See
// storage_admin.go's StorageSettingsView doc comment on the Go side for the
// full rationale (every artifact reference pinned to the old backend would
// go permanently unreachable the moment a live-edited backend took effect,
// with no warning). Only named system credentials stay live-editable — see
// useCreateSystemCredential/useDeleteSystemCredential in
// features/credentials/hooks.
export async function getStorageSettings(): Promise<StorageSettingsView> {
  return api.get<StorageSettingsView>('/api/storage/settings')
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
