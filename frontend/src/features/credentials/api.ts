export type {
  Credential,
  CredentialKind,
  CreateCredentialRequest,
  RotateCredentialRequest,
  PatchCredentialRequest,
  TestCredentialRequest,
  TestCredentialResult,
} from './types'

import { api, projectApi } from '@/lib/api'
import type {
  Credential,
  CreateCredentialRequest,
  PatchCredentialRequest,
  RotateCredentialRequest,
  TestCredentialRequest,
  TestCredentialResult,
} from './types'

export async function listCredentials(projectId: string): Promise<Credential[]> {
  const data = await projectApi(projectId).get<Credential[]>('/credentials')
  return Array.isArray(data) ? data : []
}

export async function createCredential(projectId: string, req: CreateCredentialRequest): Promise<Credential> {
  return projectApi(projectId).post<Credential>('/credentials', req)
}

export async function rotateCredential(projectId: string, name: string, req: RotateCredentialRequest): Promise<void> {
  return projectApi(projectId).post(`/credentials/${encodeURIComponent(name)}/rotate`, req)
}

export async function patchCredential(projectId: string, name: string, req: PatchCredentialRequest): Promise<Credential> {
  return projectApi(projectId).patch<Credential>(`/credentials/${encodeURIComponent(name)}`, req)
}

export async function testCredential(projectId: string, name: string, req: TestCredentialRequest): Promise<TestCredentialResult> {
  return projectApi(projectId).post<TestCredentialResult>(`/credentials/${encodeURIComponent(name)}/test`, req)
}

export async function deleteCredential(projectId: string, name: string): Promise<void> {
  return projectApi(projectId).delete(`/credentials/${encodeURIComponent(name)}`)
}

// ── System-scoped (admin) ─────────────────────────────────────────────────────
// System credentials back master-level resources such as the artifact-storage
// s3 credential. They live under the reserved system project.

const SYSTEM_BASE = '/api/system'

export async function listSystemCredentials(): Promise<Credential[]> {
  const data = await api.get<Credential[]>(`${SYSTEM_BASE}/credentials`)
  return Array.isArray(data) ? data : []
}

export async function createSystemCredential(req: CreateCredentialRequest): Promise<Credential> {
  return api.post<Credential>(`${SYSTEM_BASE}/credentials`, req)
}

export async function rotateSystemCredential(name: string, req: RotateCredentialRequest): Promise<void> {
  return api.post(`${SYSTEM_BASE}/credentials/${encodeURIComponent(name)}/rotate`, req)
}

export async function patchSystemCredential(name: string, req: PatchCredentialRequest): Promise<Credential> {
  return api.patch<Credential>(`${SYSTEM_BASE}/credentials/${encodeURIComponent(name)}`, req)
}

export async function deleteSystemCredential(name: string): Promise<void> {
  return api.delete(`${SYSTEM_BASE}/credentials/${encodeURIComponent(name)}`)
}
