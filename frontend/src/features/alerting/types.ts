export type AlertSource = 'event' | 'metric'

export interface AlertRule {
  id: string
  project_id: string
  name: string
  on: AlertSource
  event_type?: string
  when?: string
  metric_key?: string
  condition?: string
  notify: string[]
  cooldown_seconds: number
  enabled: boolean
  created_by?: string
  last_matched_at?: string
  last_attempted_at?: string
  last_success_at?: string
  last_error?: string
  created_at: string
  updated_at: string
}

export interface CreateAlertRuleRequest {
  name: string
  on: AlertSource
  event_type?: string
  when?: string
  metric_key?: string
  condition?: string
  notify: string[]
  cooldown_seconds: number
  enabled?: boolean
}

export type PatchAlertRuleRequest = Partial<CreateAlertRuleRequest>
