// workers feature API

const BASE = ''

export interface Worker {
  id: string
  label: string
  version: string
  capabilities: string
  hostname: string
  concurrency: number
  status: string
  in_flight: number
  registered_at: string
  last_seen: string
}

export async function listWorkers(): Promise<Worker[]> {
  const res = await fetch(`${BASE}/api/workers`)
  if (!res.ok) throw new Error(`listWorkers: ${res.status}`)
  return res.json()
}
