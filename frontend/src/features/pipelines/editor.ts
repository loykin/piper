import { parse as parseYAML } from 'yaml'

export type PipelineTaskType = 'command' | 'python' | 'notebook'

export interface PipelineArtifactDraft {
  name: string
  path: string
  from: string
}

export interface PipelineKeyValueDraft {
  key: string
  value: string
}

export interface PipelineStepDraft {
  id: string
  name: string
  type: PipelineTaskType
  sourcePath: string
  deps: string[]
  dependsOn: string[]
  command: string[]
  inputs: PipelineArtifactDraft[]
  outputs: PipelineArtifactDraft[]
  params: PipelineKeyValueDraft[]
  env: PipelineKeyValueDraft[]
  cpu: string
  memory: string
  gpu: string
}

export interface PipelineDraft {
  name: string
  steps: PipelineStepDraft[]
}

const DEFAULT_STEP_COMMAND = ['echo', 'hello from piper']
let nextStepSeq = 0

function nextStepId(): string {
  nextStepSeq += 1
  return `task-${nextStepSeq}`
}

function nextStepName(index = 0): string {
  return `task-${index + 1}`
}

function defaultParams(): PipelineKeyValueDraft[] {
  return []
}

function defaultArtifacts(): PipelineArtifactDraft[] {
  return []
}

export function defaultPipelineStep(index = 0, type: PipelineTaskType = 'command'): PipelineStepDraft {
  const name = nextStepName(index)
  return {
    id: nextStepId(),
    name,
    type,
    sourcePath: '',
    deps: [],
    dependsOn: index > 0 ? [nextStepName(index - 1)] : [],
    command: type === 'command' ? [...DEFAULT_STEP_COMMAND, name] : [],
    inputs: defaultArtifacts(),
    outputs: defaultArtifacts(),
    params: defaultParams(),
    env: defaultParams(),
    cpu: '',
    memory: '',
    gpu: '',
  }
}

export function defaultPipelineDraft(): PipelineDraft {
  return { name: 'my-pipeline', steps: [] }
}

export function parsePipelineDraftYaml(yaml: string): PipelineDraft {
  const document = parseYAML(yaml || '') as {
    metadata?: { name?: unknown }
    spec?: { steps?: unknown[] }
  } | null
  const rawSteps = Array.isArray(document?.spec?.steps) ? document.spec.steps : []
  const steps = rawSteps.map((value, index) => {
    const step = value as Record<string, unknown>
    const run = (step.run ?? {}) as Record<string, unknown>
    const rawType = String(run.type ?? (run.notebook ? 'notebook' : 'command'))
    const type: PipelineTaskType = rawType === 'python' || rawType === 'notebook' ? rawType : 'command'
    const artifacts = (key: 'inputs' | 'outputs'): PipelineArtifactDraft[] => {
      const values = Array.isArray(step[key]) ? step[key] : []
      return values.map(value => {
        const artifact = value as Record<string, unknown>
        return {
          name: String(artifact.name ?? ''),
          path: String(artifact.path ?? ''),
          from: String(artifact.from ?? ''),
        }
      })
    }
    const paramsObject = (step.params ?? {}) as Record<string, unknown>
    const options = (step.options ?? {}) as Record<string, unknown>
    const envValues = Array.isArray(options.env) ? options.env : []
    const driver = (step.driver ?? {}) as Record<string, unknown>
    const k8s = (driver.k8s ?? {}) as Record<string, unknown>
    const resources = (k8s.resources ?? {}) as Record<string, unknown>

    return {
      id: nextStepId(),
      name: String(step.name ?? nextStepName(index)),
      type,
      sourcePath: String(type === 'notebook' ? (run.notebook ?? run.path ?? '') : (run.path ?? '')),
      deps: Array.isArray(run.deps) ? run.deps.map(String) : [],
      dependsOn: Array.isArray(step.depends_on) ? step.depends_on.map(String) : [],
      command: Array.isArray(run.command)
        ? run.command.map(String)
        : (type === 'command' ? [...DEFAULT_STEP_COMMAND] : []),
      inputs: artifacts('inputs'),
      outputs: artifacts('outputs'),
      params: Object.entries(paramsObject).map(([key, value]) => ({ key, value: String(value) })),
      env: envValues.map(value => {
        const entry = value as Record<string, unknown>
        return { key: String(entry.name ?? ''), value: String(entry.value ?? '') }
      }),
      cpu: String(resources.cpu ?? ''),
      memory: String(resources.memory ?? ''),
      gpu: String(resources.gpu ?? ''),
    }
  })

  return { name: String(document?.metadata?.name ?? 'my-pipeline'), steps }
}

function formatArtifactBlock(key: 'inputs' | 'outputs', items: PipelineArtifactDraft[]): string[] {
  if (items.length === 0) return []
  const lines = [`      ${key}:`]
  for (const item of items) {
    lines.push(`        - name: ${JSON.stringify(item.name.trim() || 'item')}`)
    if (item.path.trim()) lines.push(`          path: ${JSON.stringify(item.path.trim())}`)
    if (item.from.trim()) lines.push(`          from: ${JSON.stringify(item.from.trim())}`)
  }
  return lines
}

function formatMapBlock(key: 'params' | 'env' | 'resources', items: Record<string, string>): string[] {
  const keys = Object.keys(items).filter(k => k.trim())
  if (keys.length === 0) return []
  const lines = [`      ${key}:`]
  for (const itemKey of keys.sort()) {
    const value = items[itemKey]
    lines.push(`        ${itemKey}: ${JSON.stringify(value)}`)
  }
  return lines
}

export function buildPipelineDraftYaml(draft: PipelineDraft): string {
  const name = draft.name.trim() || 'my-pipeline'
  const steps = draft.steps
  const lines: string[] = [
    'apiVersion: piper/v1',
    'kind: Pipeline',
    'metadata:',
    `  name: ${JSON.stringify(name)}`,
    'spec:',
    '  steps:',
  ]

  for (const step of steps) {
    const stepName = step.name.trim() || 'task'
    const dependsOn = step.dependsOn.map(dep => dep.trim()).filter(Boolean)
    const command = step.command.map(arg => arg.trim()).filter(Boolean)
    const params = Object.fromEntries(step.params.map(param => [param.key.trim(), param.value]))
    const env = Object.fromEntries(step.env.map(entry => [entry.key.trim(), entry.value]))
    const resources: Record<string, string> = {}
    if (step.cpu.trim()) resources.cpu = step.cpu.trim()
    if (step.memory.trim()) resources.memory = step.memory.trim()
    if (step.gpu.trim()) resources.gpu = step.gpu.trim()

    lines.push(`    - name: ${JSON.stringify(stepName)}`)
    if (dependsOn.length > 0) {
      lines.push(`      depends_on: [${dependsOn.map(d => JSON.stringify(d)).join(', ')}]`)
    }
    lines.push('      run:')
    lines.push(`        type: ${step.type}`)
    const deps = step.deps.map(d => d.trim()).filter(Boolean)
    if (step.type === 'notebook') {
      if (step.sourcePath.trim()) lines.push(`        notebook: ${JSON.stringify(step.sourcePath.trim())}`)
    } else {
      if (step.sourcePath.trim()) lines.push(`        path: ${JSON.stringify(step.sourcePath.trim())}`)
      if (command.length > 0) {
        lines.push('        command:')
        for (const arg of command) {
          lines.push(`          - ${JSON.stringify(arg)}`)
        }
      }
    }
    if (deps.length > 0) {
      lines.push('        deps:')
      for (const dep of deps) {
        lines.push(`          - ${JSON.stringify(dep)}`)
      }
    }
    lines.push(...formatMapBlock('params', params))
    lines.push(...formatMapBlock('env', env))
    lines.push(...formatArtifactBlock('inputs', step.inputs))
    lines.push(...formatArtifactBlock('outputs', step.outputs))
    lines.push(...formatMapBlock('resources', resources))
  }

  return `${lines.join('\n')}\n`
}

export function validatePipelineDraft(draft: PipelineDraft): string[] {
  const messages: string[] = []
  if (!draft.name.trim()) messages.push('Pipeline name is required.')
  if (draft.steps.length === 0) messages.push('At least one task is required.')

  const seen = new Set<string>()
  for (const step of draft.steps) {
    if (!step.name.trim()) {
      messages.push('Task name is required.')
      continue
    }
    if (seen.has(step.name)) {
      messages.push(`Duplicate task name: ${step.name}`)
    }
    seen.add(step.name)
    if ((step.type === 'notebook' || step.type === 'python') && !step.sourcePath.trim()) {
      messages.push(`Task "${step.name}" needs a source file.`)
    }
    if (step.type === 'command' && step.command.length === 0) {
      messages.push(`Task "${step.name}" needs a command.`)
    }
  }

  for (const step of draft.steps) {
    for (const dep of step.dependsOn) {
      if (!seen.has(dep)) messages.push(`Task "${step.name}" depends on unknown task "${dep}"`)
    }
  }

  return messages
}
