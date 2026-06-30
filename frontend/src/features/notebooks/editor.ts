// notebooks feature — YAML builder utilities
import { appendEnvOptionsYaml, type EnvVarDraft } from '@/shared/env'

export interface K8sFormState {
  name: string
  env: EnvVarDraft[]
  image: string
  cpu: string
  memory: string
  gpu: string
  storageSize: string
  prepareBackend: 'k8s'
  prepare: string
}

export const DEFAULT_K8S: K8sFormState = {
  name: '',
  env: [],
  image: '',
  cpu: '',
  memory: '',
  gpu: '',
  storageSize: '',
  prepareBackend: 'k8s',
  prepare: '',
}

export interface WorkerFormState {
  name: string
  envVars: EnvVarDraft[]
  env: string
  gpus: string
  prepareBackend: 'process' | 'docker'
  prepare: string
}

export const DEFAULT_WORKER: WorkerFormState = {
  name: '',
  envVars: [],
  env: '',
  gpus: '',
  prepareBackend: 'process',
  prepare: '',
}

export function buildK8sYAML(f: K8sFormState, workerID?: string): string {
  const lines: string[] = [
    `apiVersion: piper/v1`,
    `kind: Notebook`,
    `metadata:`,
    `  name: ${JSON.stringify(f.name || 'my-notebook')}`,
    `spec:`,
  ]

  // volume (domain — PVC size)
  appendEnvOptionsYaml(lines, '  ', f.env)
  if (f.storageSize) lines.push(`  volume:`, `    size: "${f.storageSize}"`)

  appendPrepareSteps(lines, f.prepare, f.prepareBackend)

  // driver block
  lines.push(`  driver:`)
  if (f.image) lines.push(`    image: "${f.image}"`)
  if (workerID) lines.push(`    placement:`, `      worker: ${JSON.stringify(workerID)}`)

  // K8s-specific: resources via pod_template
  const hasCpu = !!f.cpu, hasMem = !!f.memory, hasGpu = !!f.gpu
  if (hasCpu || hasMem || hasGpu) {
    lines.push(
      `    k8s:`,
      `      pod_template:`,
      `        spec:`,
      `          containers:`,
      `            - name: notebook`,
      `              resources:`,
    )
    const requests: string[] = []
    const limits: string[] = []
    if (hasCpu) requests.push(`                  cpu: "${f.cpu}"`)
    if (hasMem) { requests.push(`                  memory: "${f.memory}"`); limits.push(`                  memory: "${f.memory}"`) }
    if (hasGpu) limits.push(`                  nvidia.com/gpu: "${f.gpu}"`)
    if (requests.length) { lines.push(`                requests:`); lines.push(...requests) }
    if (limits.length)   { lines.push(`                limits:`);   lines.push(...limits) }
  }

  return lines.join('\n') + '\n'
}

export function buildWorkerYAML(f: WorkerFormState, workerID?: string): string {
  return buildWorkerYAMLWithBackend(f, workerID, f.prepareBackend)
}

export function buildWorkerYAMLWithBackend(
  f: WorkerFormState,
  workerID: string | undefined,
  backend: 'process' | 'docker' | 'k8s',
): string {
  const lines: string[] = [
    `apiVersion: piper/v1`,
    `kind: Notebook`,
    `metadata:`,
    `  name: ${JSON.stringify(f.name || 'my-notebook')}`,
    `spec:`,
  ]

  appendPrepareSteps(lines, f.prepare, backend)
  appendEnvOptionsYaml(lines, '  ', f.envVars)

  // driver block
  lines.push(`  driver:`)
  if (workerID) lines.push(`    placement:`, `      worker: ${JSON.stringify(workerID)}`)

  // process-specific GPU/env settings
  if (f.env || f.gpus) {
    lines.push(`    process:`)
    if (f.env)  lines.push(`      env: ${JSON.stringify(f.env)}`)
    if (f.gpus) lines.push(`      gpus: ${JSON.stringify(f.gpus)}`)
  }

  return lines.join('\n') + '\n'
}

export function appendPrepareSteps(lines: string[], text: string, backend: 'process' | 'docker' | 'k8s') {
  const commands = text
    .split('\n')
    .map(line => line.trim())
    .filter(Boolean)
  if (commands.length === 0) return
  lines.push(`  prepare:`, `    steps:`)
  for (const cmd of commands) {
    lines.push(
      `      - type: command`,
      `        backend: ${backend}`,
      `        command: ["sh", "-lc", "${escapeYamlDoubleQuoted(cmd)}"]`,
    )
  }
}

export function escapeYamlDoubleQuoted(value: string): string {
  return value
    .replace(/\\/g, '\\\\')
    .replace(/"/g, '\\"')
}

export function normalizePrepareBackend(mode?: string): 'process' | 'docker' {
  return mode === 'docker' ? 'docker' : 'process'
}
