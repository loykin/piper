import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { titleCase } from '../src/lib/copy.ts'

// Literal UI labels and titles in src/ must be Title Case (see
// src/lib/copy.ts). Lives outside src/ because it reads source files (Node). Sentences — anything ending in punctuation — are exempt.
const ATTR = /\b(label|title|submitLabel|eyebrow)="([^"]+)"/g
// Visible text of buttons, headings, and field labels written inline.
const TEXT = />\s*((?:[A-Za-z][\w-]*)(?: [A-Za-z][\w-]*)+)\s*<\/(Button|DialogTitle|AlertDialogTitle|Label|h[1-6])>/g

const SRC = join(import.meta.dirname, '../src')

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name: string) => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return name === 'ui' ? [] : sourceFiles(path)
    return path.endsWith('.tsx') && !path.endsWith('.test.tsx') ? [path] : []
  })
}

describe('UI copy', () => {
  it('uses Title Case for literal labels and titles', () => {
    const offenders: string[] = []
    for (const file of sourceFiles(SRC)) {
      for (const [, attr, value] of readFileSync(file, 'utf8').matchAll(ATTR)) {
        if (/[.?!…:]$/.test(value)) continue
        if (titleCase(value) !== value) offenders.push(`${file}: ${attr}="${value}" → "${titleCase(value)}"`)
      }
      for (const [, value, tag] of readFileSync(file, 'utf8').matchAll(TEXT)) {
        if (titleCase(value) !== value) offenders.push(`${file}: <${tag}>${value} → "${titleCase(value)}"`)
      }
    }
    expect(offenders).toEqual([])
  })

  it('writes the ellipsis as one character', () => {
    const offenders = sourceFiles(SRC).filter(file => /'[A-Z][a-z]+ing\.\.\.'/.test(readFileSync(file, 'utf8')))
    expect(offenders).toEqual([])
  })

  it('titleCase keeps minor words and acronyms', () => {
    expect(titleCase('deploy to schedule')).toBe('Deploy to Schedule')
    expect(titleCase('Approved by')).toBe('Approved by')
    expect(titleCase('Notebook YAML')).toBe('Notebook YAML')
  })
})
