// schedules feature API

import type { Run } from '@/features/runs/api'

const BASE = ''

export interface Schedule {
  id: string
  name: string
  owner_id?: string
  pipeline_yaml: string
  schedule_type: 'immediate' | 'once' | 'cron'
  cron_expr?: string
  enabled: boolean
  last_run_at?: string
  next_run_at: string
  created_at: string
  updated_at: string
}

export interface CreateScheduleOptions {
  name: string
  yaml: string
  type: 'immediate' | 'once' | 'cron'
  cron?: string
  run_at?: string  // ISO string for type=once
  owner_id?: string
  params?: Record<string, unknown>
}

export async function listSchedules(): Promise<Schedule[]> {
  const res = await fetch(`${BASE}/schedules`)
  if (!res.ok) throw new Error(`listSchedules: ${res.status}`)
  const data: unknown = await res.json()
  return Array.isArray(data) ? (data as Schedule[]) : []
}

export async function getSchedule(id: string): Promise<Schedule> {
  const res = await fetch(`${BASE}/schedules/${id}`)
  if (!res.ok) throw new Error(`getSchedule: ${res.status}`)
  return res.json()
}

export async function listScheduleRuns(scheduleId: string): Promise<Run[]> {
  const res = await fetch(`${BASE}/schedules/${scheduleId}/runs`)
  if (!res.ok) throw new Error(`listScheduleRuns: ${res.status}`)
  const data: unknown = await res.json()
  return Array.isArray(data) ? (data as Run[]) : []
}

export async function createSchedule(options: CreateScheduleOptions): Promise<{ schedule_id: string }> {
  const res = await fetch(`${BASE}/schedules`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(options),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
  return res.json()
}

export async function setScheduleEnabled(id: string, enabled: boolean): Promise<void> {
  const res = await fetch(`${BASE}/schedules/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ enabled }),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
}

export async function deleteSchedule(id: string): Promise<void> {
  const res = await fetch(`${BASE}/schedules/${id}`, { method: 'DELETE' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
}
