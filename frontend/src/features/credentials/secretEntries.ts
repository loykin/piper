import { z } from 'zod'
import type { CredentialKind } from './types'

export const secretEntrySchema = z.object({ key: z.string(), value: z.string() })
export type SecretEntry = z.infer<typeof secretEntrySchema>

/** The shape a form using SecretEntriesField must have. */
export type SecretEntriesValues = { entries: SecretEntry[] }

/**
 * Entries that will actually be sent: a key is required, and every kind but
 * `generic` also requires a value (generic credentials may store empty ones).
 */
export function secretEntriesPayload(kind: CredentialKind, entries: SecretEntry[]): Record<string, string> {
  return Object.fromEntries(
    entries
      .filter(e => e.key.trim() && (kind === 'generic' || e.value.trim()))
      .map(e => [e.key.trim(), e.value]),
  )
}
