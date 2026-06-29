export type {
  SecretMetadata,
  CreateSecretRequest,
  RotateSecretRequest,
  PatchSecretRequest,
  SecretProvider,
} from './types'

import { projectApi } from '@/lib/api'
import type { CreateSecretRequest, PatchSecretRequest, RotateSecretRequest, SecretMetadata } from './types'

export async function listSecrets(projectId: string): Promise<SecretMetadata[]> {
  const data = await projectApi(projectId).get<SecretMetadata[]>('/secrets')
  return Array.isArray(data) ? data : []
}

export async function createSecret(projectId: string, req: CreateSecretRequest): Promise<SecretMetadata> {
  return projectApi(projectId).post<SecretMetadata>('/secrets', req)
}

export async function rotateSecret(projectId: string, name: string, req: RotateSecretRequest): Promise<void> {
  return projectApi(projectId).post(`/secrets/${encodeURIComponent(name)}/rotate`, req)
}

export async function patchSecret(projectId: string, name: string, req: PatchSecretRequest): Promise<SecretMetadata> {
  return projectApi(projectId).patch<SecretMetadata>(`/secrets/${encodeURIComponent(name)}`, req)
}

export async function deleteSecret(projectId: string, name: string): Promise<void> {
  return projectApi(projectId).delete(`/secrets/${encodeURIComponent(name)}`)
}
