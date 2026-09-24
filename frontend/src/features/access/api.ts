import { api, projectApi, type Paged } from '@/lib/api'
import type { CreateUserRequest, MemberCandidate, ProjectMember, ProjectRole, User } from './types'

export async function listUsers(): Promise<User[]> {
  return api.getList<User>('/api/system/users')
}

/**
 * Like `listUsers`, but for a `limit`-paginated page — also returns the
 * total row count, read from the `X-Total-Count` response header the server
 * only sets when a limit was sent.
 */
export async function listUsersPaged(limit: number, offset: number): Promise<Paged<User>> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  return api.getPaged<User>(`/api/system/users?${params.toString()}`)
}

export function createUser(request: CreateUserRequest): Promise<User> {
  return api.post<User>('/api/system/users', request)
}

export function deleteUser(id: string): Promise<void> {
  return api.delete(`/api/system/users/${encodeURIComponent(id)}`)
}

export async function listUserMemberships(userId: string): Promise<ProjectMember[]> {
  return api.getList<ProjectMember>(`/api/system/users/${encodeURIComponent(userId)}/memberships`)
}

export async function listMembers(projectId: string): Promise<ProjectMember[]> {
  return projectApi(projectId).getList<ProjectMember>('/members')
}

/** Like `listMembers`, but for a `limit`-paginated page — see `listUsersPaged`. */
export async function listMembersPaged(projectId: string, limit: number, offset: number): Promise<Paged<ProjectMember>> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  return projectApi(projectId).getPaged<ProjectMember>(`/members?${params.toString()}`)
}

export async function listMemberCandidates(projectId: string): Promise<MemberCandidate[]> {
  return projectApi(projectId).getList<MemberCandidate>('/members/candidates')
}

export function addMember(projectId: string, username: string, role: ProjectRole): Promise<ProjectMember> {
  return projectApi(projectId).post<ProjectMember>('/members', { username, role })
}

export function updateMember(projectId: string, userId: string, role: ProjectRole): Promise<ProjectMember> {
  return projectApi(projectId).patch<ProjectMember>(`/members/${encodeURIComponent(userId)}`, { role })
}

export function removeMember(projectId: string, userId: string): Promise<void> {
  return projectApi(projectId).delete(`/members/${encodeURIComponent(userId)}`)
}
