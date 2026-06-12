// schedules feature API
export type { Schedule, CreateScheduleOptions } from './types'

import type { Run } from '@/features/runs/api'
import type { Schedule, CreateScheduleOptions } from './types'
import { projectApi } from '@/lib/api'

export async function listSchedules(projectId: string): Promise<Schedule[]> {
  const data = await projectApi(projectId).get<Schedule[]>('/schedules')
  return Array.isArray(data) ? data : []
}

export async function getSchedule(projectId: string, id: string): Promise<Schedule> {
  return projectApi(projectId).get<Schedule>(`/schedules/${id}`)
}

export async function listScheduleRuns(projectId: string, scheduleId: string): Promise<Run[]> {
  const data = await projectApi(projectId).get<Run[]>(`/schedules/${scheduleId}/runs`)
  return Array.isArray(data) ? data : []
}

export async function createSchedule(
  projectId: string,
  options: CreateScheduleOptions,
): Promise<{ schedule_id: string }> {
  return projectApi(projectId).post<{ schedule_id: string }>('/schedules', options)
}

export async function setScheduleEnabled(
  projectId: string,
  id: string,
  enabled: boolean,
): Promise<void> {
  return projectApi(projectId).patch(`/schedules/${id}`, { enabled })
}

export async function deleteSchedule(projectId: string, id: string): Promise<void> {
  return projectApi(projectId).delete(`/schedules/${id}`)
}

export async function backfillSchedule(
  projectId: string,
  id: string,
  from: string,
  to: string,
): Promise<{ run_ids: string[] }> {
  return projectApi(projectId).post<{ run_ids: string[] }>(`/schedules/${id}/backfill`, { from, to })
}
