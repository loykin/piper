import { describe, expect, it } from 'vitest'
import { KNOWN_STATUSES, statusTone } from './status'

// The status strings the backend sends (grep of the Go status constants).
// A status missing from STATUS_TONE silently renders neutral grey — a failed
// execution ("timed_out") looked like an idle one.
const BACKEND_STATUSES = [
  'awaiting_approval', 'bound', 'busy', 'canceled', 'cancelled', 'cancelling', 'closed', 'conflicted', 'dead',
  'degraded', 'delivered', 'delivering', 'disabled', 'done', 'failed', 'idle', 'offline', 'online', 'pending',
  'provisioning', 'queued', 'ready', 'recovering', 'released', 'restarting', 'retrying', 'running', 'scheduled',
  'skipped', 'starting', 'stopped', 'stopping', 'succeeded', 'success', 'synced', 'syncing', 'timed_out', 'healthy',
]

describe('status tones', () => {
  it('covers every backend status', () => {
    expect(BACKEND_STATUSES.filter(s => !KNOWN_STATUSES.includes(s))).toEqual([])
  })

  it('colors failures and successes consistently across resources', () => {
    expect(statusTone('timed_out')).toBe(statusTone('failed'))
    expect(statusTone('succeeded')).toBe(statusTone('success'))
    expect(statusTone('cancelled')).toBe(statusTone('canceled'))
  })
})
