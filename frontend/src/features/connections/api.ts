export type {
  Connection,
  ConnectionType,
  CreateConnectionRequest,
  RotateConnectionRequest,
  PatchConnectionRequest,
  TestConnectionRequest,
  TestConnectionResult,
} from './types'

import { projectApi } from '@/lib/api'
import type {
  Connection,
  CreateConnectionRequest,
  PatchConnectionRequest,
  RotateConnectionRequest,
  TestConnectionRequest,
  TestConnectionResult,
} from './types'

export async function listConnections(projectId: string): Promise<Connection[]> {
  const data = await projectApi(projectId).get<Connection[]>('/connections')
  return Array.isArray(data) ? data : []
}

export async function getConnection(projectId: string, name: string): Promise<Connection> {
  return projectApi(projectId).get<Connection>(`/connections/${encodeURIComponent(name)}`)
}

export async function createConnection(projectId: string, req: CreateConnectionRequest): Promise<Connection> {
  return projectApi(projectId).post<Connection>('/connections', req)
}

export async function rotateConnection(projectId: string, name: string, req: RotateConnectionRequest): Promise<void> {
  return projectApi(projectId).post(`/connections/${encodeURIComponent(name)}/rotate`, req)
}

export async function patchConnection(projectId: string, name: string, req: PatchConnectionRequest): Promise<Connection> {
  return projectApi(projectId).patch<Connection>(`/connections/${encodeURIComponent(name)}`, req)
}

export async function testConnection(projectId: string, name: string, req: TestConnectionRequest): Promise<TestConnectionResult> {
  return projectApi(projectId).post<TestConnectionResult>(`/connections/${encodeURIComponent(name)}/test`, req)
}

export async function deleteConnection(projectId: string, name: string): Promise<void> {
  return projectApi(projectId).delete(`/connections/${encodeURIComponent(name)}`)
}
