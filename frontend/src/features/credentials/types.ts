export type CredentialKind = 'generic' | 'git' | 's3'

export interface Credential {
  name: string
  kind: CredentialKind
  endpoint?: string
  keys?: string[]
  disabled: boolean
  last_used_at?: string
  last_tested_at?: string
  last_test_ok?: boolean
  last_test_message: string
  created_at: string
  updated_at: string
}

export interface CreateCredentialRequest {
  name: string
  kind: CredentialKind
  endpoint?: string
  data: Record<string, string>
}

export interface RotateCredentialRequest {
  data: Record<string, string>
}

export interface PatchCredentialRequest {
  enabled?: boolean
  endpoint?: string
}

export interface TestCredentialRequest {
  repo?: string
}

export interface TestCredentialResult {
  ok: boolean
  message: string
}
