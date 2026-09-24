import { api } from '@/lib/api'
import type { FederationMember } from './types'

export async function listFederationMembers(): Promise<FederationMember[]> {
  return api.getList<FederationMember>('/api/system/federation/members')
}
