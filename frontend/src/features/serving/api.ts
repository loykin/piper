// serving feature API
export type { Service, ServiceHistory, ServingWorkerInfo } from './types'

import type { Service, ServiceHistory, ServingWorkerInfo } from './types'

const BASE = ''

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
