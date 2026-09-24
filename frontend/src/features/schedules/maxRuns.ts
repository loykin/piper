import { z } from 'zod'

/**
 * Form field for a schedule's retention (`max_runs`): completed run records
 * to keep. Blank means 0 (keep all).
 */
export const maxRunsField = z.string().trim().refine(
  value => value === '' || (Number.isInteger(Number(value)) && Number(value) >= 0),
  'Retention must be a non-negative integer.',
)

/** Converts a validated `maxRunsField` value to the API's `max_runs`. */
export function toMaxRuns(value: string): number {
  return value.trim() === '' ? 0 : Number(value)
}
