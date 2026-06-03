const BASE = ''

export interface SystemSettings {
  artifact_store: {
    status: 'enabled' | 'disabled' | 'unavailable'
    backend?: string
    reason?: string
  }
}

export async function getSystemSettings(): Promise<SystemSettings> {
  const res = await fetch(`${BASE}/api/settings`)
  if (!res.ok) throw new Error(`getSystemSettings: ${res.status}`)
  return res.json()
}
