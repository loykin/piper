/**
 * Shared display formatters. Pages and features format dates, byte sizes,
 * and caught errors through these instead of each hand-rolling a variant.
 */

/** Go's zero time.Time serializes as 0001-01-01…; treat it like "no value". */
function parseDate(value?: string | null): Date | null {
  if (!value || value.startsWith('0001-01-01')) return null
  const ts = new Date(value)
  return Number.isNaN(ts.getTime()) ? null : ts
}

/** Locale date and time, or an em dash when absent. */
export function fmtDate(value?: string | null): string {
  return parseDate(value)?.toLocaleString() ?? '—'
}

/** Locale time of day, or an em dash when absent. */
export function fmtTime(value?: string | null): string {
  return parseDate(value)?.toLocaleTimeString() ?? '—'
}

/** Locale calendar date, or an em dash when absent. */
export function fmtDay(value?: string | null): string {
  return parseDate(value)?.toLocaleDateString() ?? '—'
}

/** Binary byte size (1536 → "1.5 KiB"). */
export function fmtBytes(size: number): string {
  if (!Number.isFinite(size) || size < 0) return '—'
  if (size < 1024) return `${size} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let value = size / 1024
  let unit = units[0]
  for (let i = 0; i < units.length; i += 1) {
    unit = units[i]
    if (value < 1024 || i === units.length - 1) break
    value /= 1024
  }
  return `${value.toFixed(value >= 10 ? 0 : 1)} ${unit}`
}

/** Message text for a caught value that may or may not be an Error. */
export function errorMessage(err: unknown, fallback?: string): string {
  if (err instanceof Error && err.message) return err.message
  return fallback ?? String(err)
}
