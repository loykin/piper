export type SecretType = 'git' | 'env'
export type SecretProvider = 'piper-managed'

export interface SecretMetadata {
  name: string
  type: SecretType
  provider: SecretProvider
  keys: string[]
  disabled: boolean
  created_at: string
  updated_at: string
  last_used_at?: string
}

export interface CreateSecretRequest {
  name: string
  type: SecretType
  provider: SecretProvider
  data: Record<string, string>
}

export interface RotateSecretRequest {
  data: Record<string, string>
}

export interface PatchSecretRequest {
  enabled: boolean
}
