export type { SystemSettings } from './types'
import type { SystemSettings } from './types'
import { api } from '@/lib/api'

export async function getSystemSettings(): Promise<SystemSettings> {
  return api.get<SystemSettings>('/api/settings')
}
