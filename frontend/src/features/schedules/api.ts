// schedules feature API
export type { Schedule, CreateScheduleOptions } from './types'

import type { Run } from '@/features/runs/api'
import type { Schedule, CreateScheduleOptions } from './types'

const BASE = ''

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

export async function triggerSchedule(id: string): Promise<{ run_id: string }> {
  const res = await fetch(`${BASE}/schedules/${id}/trigger`, { method: 'POST' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error ?? res.statusText)
  }
  return res.json()
}
