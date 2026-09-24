// schedules feature API
export type { Schedule, CreateScheduleOptions, DeployTemplateRequest } from './types'

import type { Run } from '@/features/runs/api'
import type { Schedule, CreateScheduleOptions, DeployTemplateRequest } from './types'
import { projectApi, type Paged } from '@/lib/api'

export async function listSchedules(projectId: string): Promise<Schedule[]> {
  return projectApi(projectId).getList<Schedule>('/schedules')
}

/** Like `listSchedules`, but for a `limit`-paginated page — see `listServingPaged`. */
export async function listSchedulesPaged(projectId: string, limit: number, offset: number): Promise<Paged<Schedule>> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  return projectApi(projectId).getPaged<Schedule>(`/schedules?${params.toString()}`)
}

export async function getSchedule(projectId: string, id: string): Promise<Schedule> {
  return projectApi(projectId).get<Schedule>(`/schedules/${encodeURIComponent(id)}`)
}

export async function listScheduleRuns(projectId: string, scheduleId: string): Promise<Run[]> {
  return projectApi(projectId).getList<Run>(`/schedules/${encodeURIComponent(scheduleId)}/runs`)
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
  return projectApi(projectId).patch(`/schedules/${encodeURIComponent(id)}`, { enabled })
}

export async function deleteSchedule(projectId: string, id: string): Promise<void> {
  return projectApi(projectId).delete(`/schedules/${encodeURIComponent(id)}`)
}

export async function backfillSchedule(
  projectId: string,
  id: string,
  from: string,
  to: string,
): Promise<{ run_ids: string[] }> {
  return projectApi(projectId).post<{ run_ids: string[] }>(`/schedules/${encodeURIComponent(id)}/backfill`, { from, to })
}

/** Creates a cron schedule bound to one pipeline template version. */
export async function deployTemplate(
  projectId: string,
  templateId: string,
  req: DeployTemplateRequest,
): Promise<Schedule> {
  return projectApi(projectId).post<Schedule>(`/pipeline-templates/${encodeURIComponent(templateId)}/deploy`, { enabled: true, ...req })
}
