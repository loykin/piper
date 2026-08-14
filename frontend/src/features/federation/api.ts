import { api } from '@/lib/api'
import type { FederationMember } from './types'

export async function listFederationMembers(): Promise<FederationMember[]> {
  const data = await api.get<FederationMember[]>('/api/federation/members')
  return Array.isArray(data) ? data : []
}
