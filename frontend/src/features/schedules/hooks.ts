// schedules feature hooks — React Query wrappers
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import * as api from './api'
import type { CreateScheduleOptions } from './types'
import { useProjectId } from '@/lib/projectContext'

export const scheduleKeys = {
  all: (projectId: string) => ['schedules', projectId] as const,
  list: (projectId: string) => ['schedules', projectId, 'list'] as const,
  one: (projectId: string, id: string) => ['schedules', projectId, id] as const,
  runs: (projectId: string, id: string) => ['schedules', projectId, id, 'runs'] as const,
}

export function useSchedules() {
  const projectId = useProjectId()
  return useQuery({
    queryKey: scheduleKeys.list(projectId),
    queryFn: () => api.listSchedules(projectId),
    enabled: !!projectId,
    refetchInterval: 5000,
  })
}

export function useSchedule(id: string) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: scheduleKeys.one(projectId, id),
    queryFn: () => api.getSchedule(projectId, id),
    enabled: !!projectId && !!id,
    refetchInterval: 5000,
  })
}

export function useScheduleRuns(id: string) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: scheduleKeys.runs(projectId, id),
    queryFn: () => api.listScheduleRuns(projectId, id),
    enabled: !!projectId && !!id,
    refetchInterval: 5000,
  })
}

export function useCreateSchedule() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (opts: CreateScheduleOptions) => api.createSchedule(projectId, opts),
    onSuccess: () => qc.invalidateQueries({ queryKey: scheduleKeys.all(projectId) }),
  })
}

export function useDeleteSchedule() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.deleteSchedule(projectId, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: scheduleKeys.all(projectId) }),
  })
}

export function useToggleSchedule() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      api.setScheduleEnabled(projectId, id, enabled),
    onSuccess: () => qc.invalidateQueries({ queryKey: scheduleKeys.all(projectId) }),
  })
}
