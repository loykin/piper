// notebooks feature API
export type {
  NotebookServer, NotebookVolume, NotebookWorkerInfo,
} from './types'

import type { NotebookServer, NotebookVolume, NotebookWorkerInfo } from './types'

const BASE = ''

export async function listNotebooks(): Promise<NotebookServer[]> {
  const res = await fetch(`${BASE}/notebooks`)
  if (!res.ok) throw new Error(`listNotebooks: ${res.status}`)
  const data: unknown = await res.json()
  return Array.isArray(data) ? (data as NotebookServer[]) : []
}

export async function getNotebook(name: string): Promise<NotebookServer> {
  const res = await fetch(`${BASE}/notebooks/${name}`)
  if (!res.ok) throw new Error(`getNotebook: ${res.status}`)
  return res.json()
}

export async function createNotebook(yaml: string, volumeId?: string): Promise<NotebookServer> {
  const res = await fetch(`${BASE}/notebooks`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ yaml, ...(volumeId ? { volume_id: volumeId } : {}) }),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
  return res.json()
}

export async function stopNotebook(name: string): Promise<void> {
  const res = await fetch(`${BASE}/notebooks/${name}/stop`, { method: 'POST' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
}

export async function startNotebook(name: string): Promise<NotebookServer> {
  const res = await fetch(`${BASE}/notebooks/${name}/start`, { method: 'POST' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
  return res.json()
}

/** Delete: removes the server record and releases the backing volume (work directory preserved). */
export async function deleteNotebook(name: string): Promise<void> {
  const res = await fetch(`${BASE}/notebooks/${name}`, { method: 'DELETE' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
}

/** Returns the master proxy URL for opening a notebook in the browser. */
export function notebookProxyURL(name: string): string {
  return `/notebooks/${name}/proxy/lab/`
}

export async function listNotebookVolumes(): Promise<NotebookVolume[]> {
  const res = await fetch(`${BASE}/notebook-volumes`)
  if (!res.ok) throw new Error(`listNotebookVolumes: ${res.status}`)
  const data: unknown = await res.json()
  return Array.isArray(data) ? (data as NotebookVolume[]) : []
}

/** List files inside a volume's work_dir. ext: comma-separated extensions e.g. ".py,.ipynb". */
export async function listVolumeFiles(volumeId: string, ext?: string): Promise<string[]> {
  const url = ext
    ? `${BASE}/notebook-volumes/${volumeId}/files?ext=${encodeURIComponent(ext)}`
    : `${BASE}/notebook-volumes/${volumeId}/files`
  const res = await fetch(url)
  if (!res.ok) return []
  const data: unknown = await res.json()
  return Array.isArray(data) ? (data as string[]) : []
}

/** Purge a released volume: permanently deletes the work directory and the volume record. */
export async function purgeNotebookVolume(id: string): Promise<void> {
  const res = await fetch(`${BASE}/notebook-volumes/${id}`, { method: 'DELETE' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
}

export async function listNotebookWorkers(): Promise<NotebookWorkerInfo[]> {
  const res = await fetch(`${BASE}/api/notebook-workers`)
  if (!res.ok) throw new Error(`listNotebookWorkers: ${res.status}`)
  const data: unknown = await res.json()
  return Array.isArray(data) ? (data as NotebookWorkerInfo[]) : []
}
