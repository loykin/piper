export type { Worker } from './types'
import type { Worker } from './types'
import { api } from '@/lib/api'

export async function listWorkers(): Promise<Worker[]> {
  const data = await api.get<Worker[]>('/api/workers')
  return Array.isArray(data) ? data : []
}
