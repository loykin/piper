export type {
  StorageConfig, StorageSettingsView, StorageObjectInfo, StorageUploadResult,
} from './types'

import type { StorageSettingsView, StorageObjectInfo, StorageUploadResult } from './types'
import { api, projectApi, type Paged } from '@/lib/api'

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
  return api.get<StorageSettingsView>('/api/system/storage/settings')
}

// ── Project-scoped ────────────────────────────────────────────────────────────

/** Paginated object listing — see `listNotebookVolumesPaged` for the shared shape. */
export async function listStorageObjectsPaged(
  projectId: string,
  limit: number,
  offset: number,
  prefix = '',
): Promise<Paged<StorageObjectInfo>> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  if (prefix) params.set('prefix', prefix)
  return projectApi(projectId).getPaged<StorageObjectInfo>(`/storage/objects?${params.toString()}`)
}

/** Object keys are folder-like paths: escape each segment, keep the slashes. */
function objectKeyPath(key: string): string {
  return key.split('/').map(encodeURIComponent).join('/')
}

export function storageObjectURL(projectId: string, key: string): string {
  return `/api/projects/${encodeURIComponent(projectId)}/storage/objects/${objectKeyPath(key)}`
}

export async function deleteStorageObject(projectId: string, key: string): Promise<void> {
  return projectApi(projectId).delete(`/storage/objects/${objectKeyPath(key)}`)
}

export async function uploadStorageObject(
  projectId: string,
  file: File,
  key?: string,
): Promise<StorageUploadResult> {
  const form = new FormData()
  form.set('file', file)
  if (key) form.set('key', key)
  return projectApi(projectId).upload<StorageUploadResult>('/storage/objects', form)
}
