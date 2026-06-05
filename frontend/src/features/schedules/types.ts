// schedules feature types
import type { Run } from '@/features/runs/types'
export type { Run }

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
  run_at?: string
  owner_id?: string
  params?: Record<string, unknown>
}
