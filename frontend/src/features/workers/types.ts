// workers feature types

export interface Worker {
  id: string
  label: string
  version: string
  capabilities: string
  hostname: string
  concurrency: number
  status: string
  in_flight: number
  registered_at: string
  last_seen: string
}
