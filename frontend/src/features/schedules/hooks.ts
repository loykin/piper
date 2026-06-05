// schedules feature hooks — React Query wrappers
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import * as api from './api'
import type { CreateScheduleOptions } from './types'

export const scheduleKeys = {
  all: ['schedules'] as const,
  list: () => ['schedules', 'list'] as const,
  one: (id: string) => ['schedules', id] as const,
  runs: (id: string) => ['schedules', id, 'runs'] as const,
}

export function useSchedules() {
  return useQuery({
    queryKey: scheduleKeys.list(),
    queryFn: api.listSchedules,
    refetchInterval: 5000,
  })
}

export function useSchedule(id: string) {
  return useQuery({
    queryKey: scheduleKeys.one(id),
    queryFn: () => api.getSchedule(id),
    refetchInterval: 5000,
  })
}

export function useScheduleRuns(id: string) {
  return useQuery({
    queryKey: scheduleKeys.runs(id),
    queryFn: () => api.listScheduleRuns(id),
    refetchInterval: 5000,
  })
}

export function useCreateSchedule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (opts: CreateScheduleOptions) => api.createSchedule(opts),
    onSuccess: () => qc.invalidateQueries({ queryKey: scheduleKeys.all }),
  })
}

export function useDeleteSchedule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.deleteSchedule,
    onSuccess: () => qc.invalidateQueries({ queryKey: scheduleKeys.all }),
  })
}

export function useToggleSchedule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      api.setScheduleEnabled(id, enabled),
    onSuccess: () => qc.invalidateQueries({ queryKey: scheduleKeys.all }),
  })
}

export function useTriggerSchedule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: api.triggerSchedule,
    onSuccess: () => qc.invalidateQueries({ queryKey: scheduleKeys.all }),
  })
}
