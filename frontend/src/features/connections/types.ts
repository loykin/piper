export type ConnectionType = 'git' | 'registry'

export interface Connection {
  name: string
  type: ConnectionType
  endpoint: string
  disabled: boolean
  last_used_at?: string
  last_tested_at?: string
  last_test_ok?: boolean
  last_test_message: string
  created_at: string
  updated_at: string
}

export interface CreateConnectionRequest {
  name: string
  type: ConnectionType
  endpoint: string
  data: Record<string, string>
}

export interface RotateConnectionRequest {
  data: Record<string, string>
}

export interface PatchConnectionRequest {
  enabled?: boolean
  endpoint?: string
}

export interface TestConnectionRequest {
  repo?: string
}

export interface TestConnectionResult {
  ok: boolean
  message: string
}
