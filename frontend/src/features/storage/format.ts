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

export function fmtDate(value: string): string {
  if (!value || value.startsWith('0001-01-01')) return '—'
  const ts = new Date(value)
  if (Number.isNaN(ts.getTime())) return '—'
  return ts.toLocaleString()
}
