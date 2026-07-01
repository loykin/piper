export type EnvVarSourceMode = 'value' | 'credential'

export interface EnvVarDraft {
  name: string
  value: string
  source: EnvVarSourceMode
  credentialName: string
  credentialKey: string
}

export function emptyEnvVarDraft(): EnvVarDraft {
  return { name: '', value: '', source: 'value', credentialName: '', credentialKey: '' }
}

export function envVarDraftFromYamlEntry(value: unknown): EnvVarDraft {
  const entry = (value ?? {}) as Record<string, unknown>
  const valueFrom = (entry.valueFrom ?? {}) as Record<string, unknown>
  const credentialRef = (valueFrom.credentialRef ?? {}) as Record<string, unknown>
  const credentialName = String(credentialRef.name ?? '')
  const credentialKey = String(credentialRef.key ?? '')
  if (credentialName || credentialKey) {
    return {
      name: String(entry.name ?? ''),
      value: '',
      source: 'credential',
      credentialName,
      credentialKey,
    }
  }
  return {
    name: String(entry.name ?? ''),
    value: String(entry.value ?? ''),
    source: 'value',
    credentialName: '',
    credentialKey: '',
  }
}

export function hasEnvVarValue(item: EnvVarDraft): boolean {
  const name = item.name.trim()
  if (!name) return false
  if (item.source === 'credential') return !!item.credentialName.trim() && !!item.credentialKey.trim()
  return item.value !== ''
}

export function appendEnvOptionsYaml(lines: string[], indent: string, env: EnvVarDraft[]) {
  const items = env.filter(hasEnvVarValue)
  if (items.length === 0) return
  lines.push(`${indent}options:`, `${indent}  env:`)
  for (const item of items) {
    lines.push(`${indent}    - name: ${JSON.stringify(item.name.trim())}`)
    if (item.source === 'credential') {
      lines.push(
        `${indent}      valueFrom:`,
        `${indent}        credentialRef:`,
        `${indent}          name: ${JSON.stringify(item.credentialName.trim())}`,
        `${indent}          key: ${JSON.stringify(item.credentialKey.trim())}`,
      )
    } else {
      lines.push(`${indent}      value: ${JSON.stringify(item.value)}`)
    }
  }
}
