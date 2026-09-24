import { describe, expect, it } from 'vitest'
import { fmtBytes, fmtDate } from './format'

describe('fmtDate', () => {
  it('treats Go zero time and missing values as absent', () => {
    expect(fmtDate('0001-01-01T00:00:00Z')).toBe('—')
    expect(fmtDate(undefined)).toBe('—')
    expect(fmtDate('not a date')).toBe('—')
  })
})

describe('fmtBytes', () => {
  it('uses binary units', () => {
    expect(fmtBytes(512)).toBe('512 B')
    expect(fmtBytes(1536)).toBe('1.5 KiB')
    expect(fmtBytes(20 * 1024 * 1024)).toBe('20 MiB')
    expect(fmtBytes(-1)).toBe('—')
  })
})
