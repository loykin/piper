// serving feature API

const BASE = ''

export interface Service {
  name: string
  run_id: string
  artifact: string   // "step/artifact_name"
  status: 'running' | 'stopped' | 'failed'
  endpoint: string   // e.g. "http://localhost:9001"
  namespace?: string
  pid: number
  yaml: string
  created_at: string
  updated_at: string
}

export interface ServiceHistory {
  id: number
  name: string
  run_id: string
  artifact: string
  status: string
  endpoint: string
  namespace?: string
  pid: number
  yaml: string
  deployed_at: string
  stopped_at: string
}

export interface ServingWorkerInfo {
  id: string
  addr: string
  gpus: string[]
  hostname: string
  last_seen: string
}

export async function listServing(): Promise<Service[]> {
  const res = await fetch(`${BASE}/serving`)
  if (!res.ok) throw new Error(`listServing: ${res.status}`)
  const data: unknown = await res.json()
  return Array.isArray(data) ? (data as Service[]) : []
}

export async function getServing(name: string): Promise<Service> {
  const res = await fetch(`${BASE}/serving/${name}`)
  if (!res.ok) throw new Error(`getServing: ${res.status}`)
  return res.json()
}

export async function deployServing(yaml: string): Promise<{ name: string }> {
  const res = await fetch(`${BASE}/serving`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ yaml }),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
  return res.json()
}

export async function stopServing(name: string): Promise<void> {
  const res = await fetch(`${BASE}/serving/${name}`, { method: 'DELETE' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
}

export async function restartServing(name: string): Promise<void> {
  const res = await fetch(`${BASE}/serving/${name}/restart`, { method: 'POST' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
}

export async function listServingHistory(): Promise<ServiceHistory[]> {
  const res = await fetch(`${BASE}/serving/history`)
  if (!res.ok) throw new Error(`listServingHistory: ${res.status}`)
  const data: unknown = await res.json()
  return Array.isArray(data) ? (data as ServiceHistory[]) : []
}

export async function listServingWorkers(): Promise<ServingWorkerInfo[]> {
  const res = await fetch(`${BASE}/api/serving-workers`)
  if (!res.ok) throw new Error(`listServingWorkers: ${res.status}`)
  return res.json()
}
