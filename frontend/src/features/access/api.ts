import { api, projectApi } from '@/lib/api'
import type { CreateUserRequest, ProjectMember, ProjectRole, User } from './types'

export async function listUsers(): Promise<User[]> {
  const data = await api.get<User[]>('/api/users')
  return Array.isArray(data) ? data : []
}

export function createUser(request: CreateUserRequest): Promise<User> {
  return api.post<User>('/api/users', request)
}

export function deleteUser(id: string): Promise<void> {
  return api.delete(`/api/users/${encodeURIComponent(id)}`)
}

export async function listMembers(projectId: string): Promise<ProjectMember[]> {
  const data = await projectApi(projectId).get<ProjectMember[]>('/members')
  return Array.isArray(data) ? data : []
}

export function addMember(projectId: string, userId: string, role: ProjectRole): Promise<ProjectMember> {
  return projectApi(projectId).post<ProjectMember>('/members', { user_id: userId, role })
}

export function updateMember(projectId: string, userId: string, role: ProjectRole): Promise<ProjectMember> {
  return projectApi(projectId).put<ProjectMember>(`/members/${encodeURIComponent(userId)}`, { role })
}

export function removeMember(projectId: string, userId: string): Promise<void> {
  return projectApi(projectId).delete(`/members/${encodeURIComponent(userId)}`)
}
