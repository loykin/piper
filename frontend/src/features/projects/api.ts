export type { Project, CreateProjectRequest } from './types'

import type { Project, CreateProjectRequest } from './types'
import { api } from '@/lib/api'

export async function listProjects(): Promise<Project[]> {
  const data = await api.get<Project[]>('/api/projects')
  return Array.isArray(data) ? data : []
}

export async function getProject(id: string): Promise<Project> {
  return api.get<Project>(`/api/projects/${encodeURIComponent(id)}`)
}

export async function createProject(req: CreateProjectRequest): Promise<Project> {
  return api.post<Project>('/api/projects', req)
}

export async function deleteProject(id: string): Promise<void> {
  return api.delete(`/api/projects/${encodeURIComponent(id)}`)
}
