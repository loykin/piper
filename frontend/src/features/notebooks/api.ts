// notebooks feature API
export type {
  NotebookServer, NotebookVolume, NotebookWorkerInfo,
} from './types'

import type { NotebookServer, NotebookVolume } from './types'
import { projectApi } from '@/lib/api'

export async function listNotebooks(projectId: string): Promise<NotebookServer[]> {
  const data = await projectApi(projectId).get<NotebookServer[]>('/notebooks')
  return Array.isArray(data) ? data : []
}

export async function getNotebook(projectId: string, name: string): Promise<NotebookServer> {
  return projectApi(projectId).get<NotebookServer>(`/notebooks/${name}`)
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
  return projectApi(projectId).post(`/notebooks/${name}/stop`)
}

export async function startNotebook(projectId: string, name: string): Promise<NotebookServer> {
  return projectApi(projectId).post<NotebookServer>(`/notebooks/${name}/start`)
}

export async function deleteNotebook(projectId: string, name: string): Promise<void> {
  return projectApi(projectId).delete(`/notebooks/${name}`)
}

/** Browser proxy URL for opening a notebook in the browser. */
export function notebookProxyURL(projectId: string, name: string): string {
  return `/projects/${encodeURIComponent(projectId)}/notebooks/${name}/proxy/lab/`
}

export async function listNotebookVolumes(projectId: string): Promise<NotebookVolume[]> {
  const data = await projectApi(projectId).get<NotebookVolume[]>('/notebook-volumes')
  return Array.isArray(data) ? data : []
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
    ? `/notebook-volumes/${volumeId}/files?ext=${encodeURIComponent(ext)}`
    : `/notebook-volumes/${volumeId}/files`

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
  return projectApi(projectId).delete(`/notebook-volumes/${id}`)
}
