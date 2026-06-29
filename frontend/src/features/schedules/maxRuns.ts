export function parseMaxRuns(value: string): number | null {
  const parsed = value.trim() === '' ? 0 : Number(value)
  if (!Number.isInteger(parsed) || parsed < 0) {
    return null
  }
  return parsed
}
