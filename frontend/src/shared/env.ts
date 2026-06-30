export type EnvVarSourceMode = 'value' | 'secret'

export interface EnvVarDraft {
  name: string
  value: string
  source: EnvVarSourceMode
  secretName: string
  secretKey: string
}

export function emptyEnvVarDraft(): EnvVarDraft {
  return { name: '', value: '', source: 'value', secretName: '', secretKey: '' }
}

export function envVarDraftFromYamlEntry(value: unknown): EnvVarDraft {
  const entry = (value ?? {}) as Record<string, unknown>
  const valueFrom = (entry.valueFrom ?? {}) as Record<string, unknown>
  const secretKeyRef = (valueFrom.secretKeyRef ?? {}) as Record<string, unknown>
  const secretName = String(secretKeyRef.name ?? '')
  const secretKey = String(secretKeyRef.key ?? '')
  if (secretName || secretKey) {
    return {
      name: String(entry.name ?? ''),
      value: '',
      source: 'secret',
      secretName,
      secretKey,
    }
  }
  return {
    name: String(entry.name ?? ''),
    value: String(entry.value ?? ''),
    source: 'value',
    secretName: '',
    secretKey: '',
  }
}

export function hasEnvVarValue(item: EnvVarDraft): boolean {
  const name = item.name.trim()
  if (!name) return false
  if (item.source === 'secret') return !!item.secretName.trim() && !!item.secretKey.trim()
  return item.value !== ''
}

export function appendEnvOptionsYaml(lines: string[], indent: string, env: EnvVarDraft[]) {
  const items = env.filter(hasEnvVarValue)
  if (items.length === 0) return
  lines.push(`${indent}options:`, `${indent}  env:`)
  for (const item of items) {
    lines.push(`${indent}    - name: ${JSON.stringify(item.name.trim())}`)
    if (item.source === 'secret') {
      lines.push(
        `${indent}      valueFrom:`,
        `${indent}        secretKeyRef:`,
        `${indent}          name: ${JSON.stringify(item.secretName.trim())}`,
        `${indent}          key: ${JSON.stringify(item.secretKey.trim())}`,
      )
    } else {
      lines.push(`${indent}      value: ${JSON.stringify(item.value)}`)
    }
  }
}
