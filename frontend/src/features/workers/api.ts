// workers feature API
export type { Worker } from './types'

import type { Worker } from './types'

const BASE = ''

export async function listWorkers(): Promise<Worker[]> {
  const res = await fetch(`${BASE}/api/workers`)
  if (!res.ok) throw new Error(`listWorkers: ${res.status}`)
  return res.json()
}
