// schedules feature types
import type { Run } from '@/features/runs/types'
export type { Run }

export interface Schedule {
  id: string
  name: string
  pipeline_yaml: string
  template_version_id?: string
  schedule_type: 'immediate' | 'once' | 'cron'
  cron_expr?: string
  enabled: boolean
  max_runs: number
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
  max_runs?: number
  params?: Record<string, unknown>
}

/** Body of POST /pipeline-templates/:id/deploy — a cron schedule bound to one template version. */
export interface DeployTemplateRequest {
  cron: string
  enabled?: boolean
  max_runs?: number
  params?: Record<string, unknown>
}
