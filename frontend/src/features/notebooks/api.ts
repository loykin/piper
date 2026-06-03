// notebooks feature API

const BASE = ''

export interface NotebookServer {
  name: string
  status: 'provisioning' | 'starting' | 'running' | 'stopping' | 'stopped' | 'failed'
  env: string      // bare-metal: venv path or "conda:env-name"
  image: string    // k8s: container image
  endpoint: string
  pid: number
  work_dir: string
  token: string
  worker_id?: string
  volume_id: string
  yaml: string
  created_at: string
  updated_at: string
}

export interface NotebookVolume {
  id: string
  label: string
  work_dir: string
  status: 'bound' | 'released'
  worker_id: string   // node affinity; empty for network storage (e.g. K8s CSI)
  created_at: string
  updated_at: string
}

export interface NotebookWorkerInfo {
  id: string
  kind: 'k8s' | 'baremetal'
  mode?: 'process' | 'docker'
  hostname: string
  cluster_name?: string
  gpus: string[]
  labels?: Record<string, string>
  last_seen: string
}

export type NotebookPromotionTarget = 'draft' | 'download' | 'repo' | 'object_store'

export interface NotebookPromotionValidation {
  status: 'ok' | 'warning' | 'error'
  messages: string[]
}

export interface NotebookPromotionSource {
  type: 'notebook'
  notebook_name: string
  notebook_path?: string
  git_sha?: string
  run_id?: string
  notebook_yaml?: string
}

export interface NotebookPromotionRuntime {
  worker_id?: string
  backend?: string
  mode?: string
  image?: string
  env?: string
  work_dir?: string
  endpoint?: string
}

export interface NotebookPromotionPreview {
  name: string
  target: NotebookPromotionTarget
  source: NotebookPromotionSource
  runtime: NotebookPromotionRuntime
  validation: NotebookPromotionValidation
  draft: string
}

export interface NotebookPromotionExportResult extends NotebookPromotionPreview {
  bundle_path?: string
  object_key?: string
  exported_at: string
}

export interface NotebookPromotionExportRecord extends NotebookPromotionExportResult {
  manifest_path?: string
}

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
  // 201 Created is the expected response for the async path.
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

/** Returns the master proxy URL for opening a notebook in the browser.
 *  The master proxy handles notebook auth/token forwarding. */
export function notebookProxyURL(name: string): string {
  return `/notebooks/${name}/proxy/lab/`
}

export async function listNotebookVolumes(): Promise<NotebookVolume[]> {
  const res = await fetch(`${BASE}/notebook-volumes`)
  if (!res.ok) throw new Error(`listNotebookVolumes: ${res.status}`)
  const data: unknown = await res.json()
  return Array.isArray(data) ? (data as NotebookVolume[]) : []
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

function promotionTargetQuery(target: NotebookPromotionTarget): string {
  return `?target=${encodeURIComponent(target)}`
}

export async function getNotebookPromotion(name: string, target: NotebookPromotionTarget = 'draft'): Promise<NotebookPromotionPreview> {
  const res = await fetch(`${BASE}/api/notebooks/${name}/promotion${promotionTargetQuery(target)}`)
  if (!res.ok) throw new Error(`getNotebookPromotion: ${res.status}`)
  return res.json()
}

export async function validateNotebookPromotion(name: string, target: NotebookPromotionTarget = 'draft'): Promise<NotebookPromotionValidation> {
  const res = await fetch(`${BASE}/api/notebooks/${name}/promotion/validate`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ target }),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
  return res.json()
}

export async function exportNotebookPromotion(name: string, target: NotebookPromotionTarget = 'draft'): Promise<NotebookPromotionExportResult> {
  const res = await fetch(`${BASE}/api/notebooks/${name}/promotion/export`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ target }),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
  return res.json()
}

export async function listNotebookPromotionExports(name: string): Promise<NotebookPromotionExportRecord[]> {
  const res = await fetch(`${BASE}/api/notebooks/${name}/promotion/exports`)
  if (!res.ok) throw new Error(`listNotebookPromotionExports: ${res.status}`)
  const data: unknown = await res.json()
  return Array.isArray(data) ? (data as NotebookPromotionExportRecord[]) : []
}

export async function downloadNotebookPromotionExport(name: string, file: string): Promise<void> {
  window.location.assign(`${BASE}/api/notebooks/${name}/promotion/exports/${encodeURIComponent(file)}`)
}
