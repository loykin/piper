import { parse as parseYAML } from 'yaml'
import {
  appendEnvOptionsYaml,
  envVarDraftFromYamlEntry,
  type EnvVarDraft,
} from '@/shared/env'

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

export interface PipelineDriverDraft {
  placementRuntime: string
  k8sImage: string
  k8sNamespace: string
  k8sReplicas: string
  k8sImagePullPolicy: string
  dockerImage: string
  dockerCPUs: string
  dockerMemLimit: string
  dockerShmSize: string
  dockerUser: string
  dockerNetworkMode: string
  processEnv: string
  processGPUs: string
}

export interface PipelineDefaultsDraft extends PipelineDriverDraft {
  cpu: string
  memory: string
  gpu: string
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
  env: EnvVarDraft[]
  cpu: string
  memory: string
  gpu: string
  driver: PipelineDriverDraft
}

export interface PipelineSourceDraft {
  type: 'git'
  repo: string
  branch: string
  credentialRef: string
}

export interface PipelineDraft {
  name: string
  steps: PipelineStepDraft[]
  source?: PipelineSourceDraft
  defaults: PipelineDefaultsDraft
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

function defaultEnv(): EnvVarDraft[] {
  return []
}

function defaultArtifacts(): PipelineArtifactDraft[] {
  return []
}

export function defaultPipelineDriver(): PipelineDriverDraft {
  return {
    placementRuntime: '',
    k8sImage: '',
    k8sNamespace: '',
    k8sReplicas: '',
    k8sImagePullPolicy: '',
    dockerImage: '',
    dockerCPUs: '',
    dockerMemLimit: '',
    dockerShmSize: '',
    dockerUser: '',
    dockerNetworkMode: '',
    processEnv: '',
    processGPUs: '',
  }
}

export function defaultPipelineDefaults(): PipelineDefaultsDraft {
  return { ...defaultPipelineDriver(), cpu: '', memory: '', gpu: '' }
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
    env: defaultEnv(),
    cpu: '',
    memory: '',
    gpu: '',
    driver: defaultPipelineDriver(),
  }
}

export function defaultPipelineDraft(): PipelineDraft {
  return { name: 'my-pipeline', steps: [], defaults: defaultPipelineDefaults() }
}

function stringValue(value: unknown): string {
  return value == null ? '' : String(value)
}

function parseDriver(value: unknown): PipelineDefaultsDraft {
  const driver = (value ?? {}) as Record<string, unknown>
  const placement = (driver.placement ?? {}) as Record<string, unknown>
  const k8s = (driver.k8s ?? {}) as Record<string, unknown>
  const resources = (k8s.resources ?? {}) as Record<string, unknown>
  const docker = (driver.docker ?? {}) as Record<string, unknown>
  const process = (driver.process ?? {}) as Record<string, unknown>
  return {
    placementRuntime: stringValue(placement.runtime),
    k8sImage: stringValue(k8s.image),
    k8sNamespace: stringValue(k8s.namespace),
    k8sReplicas: stringValue(k8s.replicas),
    k8sImagePullPolicy: stringValue(k8s.image_pull_policy),
    dockerImage: stringValue(docker.image),
    dockerCPUs: stringValue(docker.cpus),
    dockerMemLimit: stringValue(docker.mem_limit),
    dockerShmSize: stringValue(docker.shm_size),
    dockerUser: stringValue(docker.user),
    dockerNetworkMode: stringValue(docker.network_mode),
    processEnv: stringValue(process.env),
    processGPUs: stringValue(process.gpus),
    cpu: stringValue(resources.cpu),
    memory: stringValue(resources.memory),
    gpu: stringValue(resources.gpu),
  }
}

export function parsePipelineDraftYaml(yaml: string): PipelineDraft {
  const document = parseYAML(yaml || '') as {
    metadata?: { name?: unknown }
    spec?: { defaults?: { driver?: unknown }, steps?: unknown[] }
  } | null
  const rawSteps = Array.isArray(document?.spec?.steps) ? document.spec.steps : []
  const steps = rawSteps.map((value, index) => {
    const step = value as Record<string, unknown>
    const run = (step.run ?? {}) as Record<string, unknown>
    const rawType = String(run.type ?? (run.notebook ? 'notebook' : 'command'))
    const type: PipelineTaskType = rawType === 'python' || rawType === 'notebook' ? rawType : 'command'
    const stepLabel = String(step.name ?? nextStepName(index))
    const artifacts = (key: 'inputs' | 'outputs'): PipelineArtifactDraft[] => {
      const values = Array.isArray(step[key]) ? step[key] : []
      return values.map((value, artifactIndex) => {
        if (value === null || typeof value !== 'object' || Array.isArray(value)) {
          throw new Error(
            `Step "${stepLabel}": ${key}[${artifactIndex}] must be an artifact object ({ name, path, from }), not a bare value like ${JSON.stringify(value)}.`,
          )
        }
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
    const parsedDriver = parseDriver(step.driver)
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
      env: envValues.map(envVarDraftFromYamlEntry),
      cpu: parsedDriver.cpu,
      memory: parsedDriver.memory,
      gpu: parsedDriver.gpu,
      driver: parsedDriver,
    }
  })

  const sourceStep = rawSteps
    .map(value => ((value as Record<string, unknown>).run ?? {}) as Record<string, unknown>)
    .find(run => String(run.source ?? '') === 'git')
  const source = sourceStep ? {
    type: 'git' as const,
    repo: String(sourceStep.repo ?? ''),
    branch: String(sourceStep.branch ?? ''),
    credentialRef: String(sourceStep.credentialRef ?? ''),
  } : undefined

  return {
    name: String(document?.metadata?.name ?? 'my-pipeline'),
    steps,
    source,
    defaults: parseDriver(document?.spec?.defaults?.driver),
  }
}

function firstYamlDifference(source: unknown, generated: unknown, path = '$'): string | null {
  if (Object.is(source, generated)) return null

  if (Array.isArray(source) || Array.isArray(generated)) {
    if (!Array.isArray(source) || !Array.isArray(generated)) return path
    if (source.length !== generated.length) return path
    for (let index = 0; index < source.length; index += 1) {
      const difference = firstYamlDifference(source[index], generated[index], `${path}[${index}]`)
      if (difference) return difference
    }
    return null
  }

  if (
    source !== null && generated !== null
    && typeof source === 'object' && typeof generated === 'object'
  ) {
    const sourceObject = source as Record<string, unknown>
    const generatedObject = generated as Record<string, unknown>
    const keys = Array.from(new Set([
      ...Object.keys(sourceObject),
      ...Object.keys(generatedObject),
    ])).sort()
    for (const key of keys) {
      if (!(key in sourceObject) || !(key in generatedObject)) {
        return path === '$' ? key : `${path}.${key}`
      }
      const childPath = path === '$' ? key : `${path}.${key}`
      const difference = firstYamlDifference(sourceObject[key], generatedObject[key], childPath)
      if (difference) return difference
    }
    return null
  }

  // Any scalar change is lossy. Return only the path so credential-backed
  // environment values never leak into an error message.
  return path
}

/**
 * Returns the first semantic value that the Design editor would change or
 * discard when it regenerates YAML. Formatting, comments, quoting, and map
 * key order are intentionally ignored by comparing parsed YAML values.
 */
export function findPipelineDraftYamlDifference(yaml: string, draft: PipelineDraft): string | null {
  const source = parseYAML(yaml || '') as unknown
  const generated = parseYAML(buildPipelineDraftYaml(draft)) as unknown
  return firstYamlDifference(source, generated)
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

function appendDriverYaml(
  lines: string[],
  indent: string,
  driver: PipelineDriverDraft,
  resources: { cpu: string, memory: string, gpu: string },
) {
  const placement = {
    runtime: driver.placementRuntime.trim(),
  }
  const k8s = {
    image: driver.k8sImage.trim(),
    namespace: driver.k8sNamespace.trim(),
    replicas: driver.k8sReplicas.trim(),
    image_pull_policy: driver.k8sImagePullPolicy.trim(),
  }
  const docker = {
    image: driver.dockerImage.trim(),
    cpus: driver.dockerCPUs.trim(),
    mem_limit: driver.dockerMemLimit.trim(),
    shm_size: driver.dockerShmSize.trim(),
    user: driver.dockerUser.trim(),
    network_mode: driver.dockerNetworkMode.trim(),
  }
  const process = {
    env: driver.processEnv.trim(),
    gpus: driver.processGPUs.trim(),
  }
  const resourceValues = {
    cpu: resources.cpu.trim(),
    memory: resources.memory.trim(),
    gpu: resources.gpu.trim(),
  }
  const hasPlacement = Object.values(placement).some(Boolean)
  const hasResources = Object.values(resourceValues).some(Boolean)
  const hasK8s = Object.values(k8s).some(Boolean) || hasResources
  const hasDocker = Object.values(docker).some(Boolean)
  const hasProcess = Object.values(process).some(Boolean)
  if (!hasPlacement && !hasK8s && !hasDocker && !hasProcess) return

  lines.push(`${indent}driver:`)
  if (hasPlacement) {
    lines.push(`${indent}  placement:`)
    for (const [key, value] of Object.entries(placement)) {
      if (value) lines.push(`${indent}    ${key}: ${JSON.stringify(value)}`)
    }
  }
  if (hasK8s) {
    lines.push(`${indent}  k8s:`)
    for (const [key, value] of Object.entries(k8s)) {
      if (!value) continue
      lines.push(`${indent}    ${key}: ${key === 'replicas' && /^\d+$/.test(value) ? value : JSON.stringify(value)}`)
    }
    if (hasResources) {
      lines.push(`${indent}    resources:`)
      for (const [key, value] of Object.entries(resourceValues)) {
        if (value) lines.push(`${indent}      ${key}: ${JSON.stringify(value)}`)
      }
    }
  }
  if (hasDocker) {
    lines.push(`${indent}  docker:`)
    for (const [key, value] of Object.entries(docker)) {
      if (value) lines.push(`${indent}    ${key}: ${JSON.stringify(value)}`)
    }
  }
  if (hasProcess) {
    lines.push(`${indent}  process:`)
    for (const [key, value] of Object.entries(process)) {
      if (value) lines.push(`${indent}    ${key}: ${JSON.stringify(value)}`)
    }
  }
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
  ]
  const defaultsLines: string[] = []
  appendDriverYaml(defaultsLines, '    ', draft.defaults, draft.defaults)
  if (defaultsLines.length > 0) {
    lines.push('  defaults:', ...defaultsLines)
  }
  lines.push('  steps:')

  for (const step of steps) {
    const stepName = step.name.trim() || 'task'
    const dependsOn = step.dependsOn.map(dep => dep.trim()).filter(Boolean)
    const command = step.command.map(arg => arg.trim()).filter(Boolean)
    const params = Object.fromEntries(step.params.map(param => [param.key.trim(), param.value]))
    lines.push(`    - name: ${JSON.stringify(stepName)}`)
    if (dependsOn.length > 0) {
      lines.push(`      depends_on: [${dependsOn.map(d => JSON.stringify(d)).join(', ')}]`)
    }
    lines.push('      run:')
    lines.push(`        type: ${step.type}`)
    if (draft.source?.type === 'git') {
      lines.push(`        source: git`)
      lines.push(`        repo: ${JSON.stringify(draft.source.repo)}`)
      if (draft.source.branch.trim()) lines.push(`        branch: ${JSON.stringify(draft.source.branch.trim())}`)
      if (draft.source.credentialRef.trim()) lines.push(`        credentialRef: ${JSON.stringify(draft.source.credentialRef.trim())}`)
    }
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
    appendEnvOptionsYaml(lines, '      ', step.env)
    lines.push(...formatArtifactBlock('inputs', step.inputs))
    lines.push(...formatArtifactBlock('outputs', step.outputs))
    appendDriverYaml(lines, '      ', step.driver, step)
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
