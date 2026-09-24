// notebooks feature API
export type {
  NotebookServer, NotebookVolume, NotebookHistory,
} from './types'

import type { NotebookServer, NotebookVolume, NotebookHistory } from './types'
import { projectApi, type Paged } from '@/lib/api'

export async function listNotebooks(projectId: string): Promise<NotebookServer[]> {
  return projectApi(projectId).getList<NotebookServer>('/notebooks')
}

export async function listNotebooksPaged(projectId: string, limit: number, offset: number): Promise<Paged<NotebookServer>> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  return projectApi(projectId).getPaged<NotebookServer>(`/notebooks?${params.toString()}`)
}

export async function getNotebook(projectId: string, name: string): Promise<NotebookServer> {
  return projectApi(projectId).get<NotebookServer>(`/notebooks/${encodeURIComponent(name)}`)
}

export async function createNotebook(
  projectId: string,
  yaml: string,
  volumeId?: string,
): Promise<NotebookServer> {
  return projectApi(projectId).post<NotebookServer>('/notebooks', {
    yaml,
    ...(volumeId ? { volume_id: volumeId } : {}),
  })
}

export async function stopNotebook(projectId: string, name: string): Promise<void> {
  return projectApi(projectId).post(`/notebooks/${encodeURIComponent(name)}/stop`)
}

export async function startNotebook(projectId: string, name: string): Promise<NotebookServer> {
  return projectApi(projectId).post<NotebookServer>(`/notebooks/${encodeURIComponent(name)}/start`)
}

export async function deleteNotebook(projectId: string, name: string): Promise<void> {
  return projectApi(projectId).delete(`/notebooks/${encodeURIComponent(name)}`)
}

/** Like `listNotebooks`, but for a `limit`-paginated page — see `listServingPaged`. */
export async function listNotebookHistoryPaged(projectId: string, limit: number, offset: number): Promise<Paged<NotebookHistory>> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  return projectApi(projectId).getPaged<NotebookHistory>(`/notebooks/history?${params.toString()}`)
}

/** Browser proxy URL for opening a notebook in the browser. */
export function notebookProxyURL(projectId: string, name: string): string {
  return `/projects/${encodeURIComponent(projectId)}/notebooks/${encodeURIComponent(name)}/proxy/lab/`
}

export async function listNotebookVolumes(projectId: string): Promise<NotebookVolume[]> {
  return projectApi(projectId).getList<NotebookVolume>('/notebook-volumes')
}

/** Like `listNotebookVolumes`, but for a `limit`-paginated page — see `listServingPaged`. */
export async function listNotebookVolumesPaged(projectId: string, limit: number, offset: number): Promise<Paged<NotebookVolume>> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  return projectApi(projectId).getPaged<NotebookVolume>(`/notebook-volumes?${params.toString()}`)
}

export type VolumeFilesResult = {
  files: string[]
  truncated: boolean
  state: 'ready' | 'transitioning' | 'unavailable'
  retryAfterMs: number
}

export async function listVolumeFiles(
  projectId: string,
  volumeId: string,
  ext?: string,
): Promise<VolumeFilesResult> {
  const path = ext
    ? `/notebook-volumes/${encodeURIComponent(volumeId)}/files?ext=${encodeURIComponent(ext)}`
    : `/notebook-volumes/${encodeURIComponent(volumeId)}/files`

  const url = `/api/projects/${encodeURIComponent(projectId)}${path}`
  const res = await fetch(url)

  if (res.status === 503) {
    const retryAfter = parseInt(res.headers.get('Retry-After') ?? '2', 10)
    return { files: [], truncated: false, state: 'transitioning', retryAfterMs: retryAfter * 1000 }
  }
  if (res.status === 409) {
    return { files: [], truncated: false, state: 'unavailable', retryAfterMs: 0 }
  }
  if (!res.ok) {
    return { files: [], truncated: false, state: 'unavailable', retryAfterMs: 0 }
  }

  const data: unknown = await res.json()
  const files = Array.isArray(data) ? (data as string[]) : []
  const truncated = res.headers.get('X-Piper-Files-Truncated') === 'true'
  return { files, truncated, state: 'ready', retryAfterMs: 0 }
}

export async function purgeNotebookVolume(projectId: string, id: string): Promise<void> {
  return projectApi(projectId).delete(`/notebook-volumes/${encodeURIComponent(id)}`)
}
