export type {
  Credential,
  CredentialKind,
  CreateCredentialRequest,
  RotateCredentialRequest,
  PatchCredentialRequest,
  TestCredentialRequest,
  TestCredentialResult,
} from './types'

import { projectApi } from '@/lib/api'
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
