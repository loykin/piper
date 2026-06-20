import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { listProjects, createProject, deleteProject } from './api'
import type { CreateProjectRequest } from './types'

export const projectKeys = {
  all: () => ['projects'] as const,
  list: () => ['projects', 'list'] as const,
  one: (id: string) => ['projects', id] as const,
}

export function useProjects(enabled = true) {
  return useQuery({
    queryKey: projectKeys.list(),
    queryFn: listProjects,
    enabled,
    staleTime: 30_000,
  })
}

export function useCreateProject() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (req: CreateProjectRequest) => createProject(req),
    onSuccess: () => qc.invalidateQueries({ queryKey: projectKeys.all() }),
  })
}

export function useDeleteProject() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => deleteProject(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: projectKeys.all() }),
  })
}
