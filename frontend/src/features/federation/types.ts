export type FederationMemberStatus = 'online' | 'offline'

export interface FederationMember {
  home_id: string
  id: string
  enabled: boolean
  status: FederationMemberStatus
  last_connected_at: string | null
  last_disconnected_at: string | null
  created_at: string
  updated_at: string
}
