import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useProjectId } from '@/lib/projectContext'
import {
  addMember, createUser, deleteUser, listMembers, listUsers, removeMember, updateMember,
} from './api'
import type { CreateUserRequest, ProjectRole } from './types'

export const accessKeys = {
  users: () => ['access', 'users'] as const,
  members: (projectId: string) => ['access', projectId, 'members'] as const,
}

export function useUsers(enabled = true) {
  return useQuery({ queryKey: accessKeys.users(), queryFn: listUsers, enabled })
}

export function useCreateUser() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (request: CreateUserRequest) => createUser(request),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: accessKeys.users() }),
  })
}

export function useDeleteUser() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: deleteUser,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: accessKeys.users() }),
  })
}

export function useMembers() {
  const projectId = useProjectId()
  return useQuery({
    queryKey: accessKeys.members(projectId),
    queryFn: () => listMembers(projectId),
    enabled: !!projectId,
  })
}

export function useAddMember() {
  const projectId = useProjectId()
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ userId, role }: { userId: string, role: ProjectRole }) => addMember(projectId, userId, role),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: accessKeys.members(projectId) }),
  })
}

export function useUpdateMember() {
  const projectId = useProjectId()
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ userId, role }: { userId: string, role: ProjectRole }) => updateMember(projectId, userId, role),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: accessKeys.members(projectId) }),
  })
}

export function useRemoveMember() {
  const projectId = useProjectId()
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (userId: string) => removeMember(projectId, userId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: accessKeys.members(projectId) }),
  })
}
