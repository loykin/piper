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

function trimQuotes(value: string): string {
  return value.trim().replace(/^['"]|['"]$/g, '')
}

function parseInlineList(input: string): string[] {
  return input
    .split(',')
    .map(part => trimQuotes(part))
    .filter(Boolean)
}

function parseMapBlock(block: string, key: string): Record<string, string> {
  const out: Record<string, string> = {}
  const match = block.match(new RegExp(`^\\s*${key}:([\\s\\S]*?)(?=\\n\\s+\\w|\\n\\s*-\\s+name:|$)`, 'm'))
  if (!match) return out
  for (const m of match[1].matchAll(/^\s*([A-Za-z0-9_.-]+):\s*(.+)$/gm)) {
    const k = trimQuotes(m[1])
    const v = trimQuotes(m[2])
    if (k) out[k] = v
  }
  return out
}

function parseArtifactsBlock(block: string, key: 'inputs' | 'outputs'): PipelineArtifactDraft[] {
  const match = block.match(new RegExp(`^\\s*${key}:([\\s\\S]*?)(?=\\n\\s+\\w|\\n\\s*-\\s+name:|$)`, 'm'))
  if (!match) return []
  const section = match[1].trim()
  if (!section) return []
  const chunks = section.split(/\n(?=\s*-\s+name:)/)
  const items: PipelineArtifactDraft[] = []
  for (const chunk of chunks) {
    const nameMatch = chunk.match(/^\s*-\s+name:\s*(.+)$/m)
    if (!nameMatch) continue
    const pathMatch = chunk.match(/^\s+path:\s*(.+)$/m)
    const fromMatch = chunk.match(/^\s+from:\s*(.+)$/m)
    items.push({
      name: trimQuotes(nameMatch[1]),
      path: trimQuotes(pathMatch?.[1] ?? ''),
      from: trimQuotes(fromMatch?.[1] ?? ''),
    })
  }
  return items
}

function parseTaskType(block: string): PipelineTaskType {
  const typeMatch = block.match(/^\s*type:\s*(.+)$/m)
  const type = trimQuotes(typeMatch?.[1] ?? 'command')
  if (type === 'python' || type === 'notebook' || type === 'command') return type
  return 'command'
}

function parseSourcePath(block: string, type: PipelineTaskType): string {
  const notebookMatch = block.match(/^\s*notebook:\s*(.+)$/m)
  if (type === 'notebook') return trimQuotes(notebookMatch?.[1] ?? '')
  const pathMatch = block.match(/^\s*path:\s*(.+)$/m)
  return trimQuotes(pathMatch?.[1] ?? '')
}

function parseDeps(block: string): string[] {
  const deps: string[] = []
  const inlineDeps = block.match(/deps:\s*\[([^\]]*)]/)
  if (inlineDeps) {
    deps.push(...parseInlineList(inlineDeps[1]))
  } else {
    const depsBlock = block.match(/deps:([\s\S]*?)(?=\n\s+\w|\n\s*-\s+name:|$)/)
    if (depsBlock) {
      for (const m of depsBlock[1].matchAll(/^\s*-\s+(.+)$/gm)) {
        const dep = trimQuotes(m[1])
        if (dep) deps.push(dep)
      }
    }
  }
  return deps
}

function parseCommand(block: string): string[] {
  const command: string[] = []
  const inlineCommand = block.match(/command:\s*\[([^\]]*)]/)
  if (inlineCommand) {
    command.push(...parseInlineList(inlineCommand[1]))
  } else {
    const commandBlock = block.match(/command:([\s\S]*?)(?=\n\s+\w|\n\s*-\s+name:|$)/)
    if (commandBlock) {
      for (const m of commandBlock[1].matchAll(/^\s*-\s+(.+)$/gm)) {
        const arg = trimQuotes(m[1])
        if (arg) command.push(arg)
      }
    }
  }
  return command
}

function parseStepFields(block: string): Omit<PipelineStepDraft, 'id' | 'name'> {
  const type = parseTaskType(block)
  const sourcePath = parseSourcePath(block, type)
  const deps = parseDeps(block)
  const command = parseCommand(block)
  const params = Object.entries(parseMapBlock(block, 'params')).map(([key, value]) => ({ key, value }))
  const env = Object.entries(parseMapBlock(block, 'env')).map(([key, value]) => ({ key, value }))
  const inputs = parseArtifactsBlock(block, 'inputs')
  const outputs = parseArtifactsBlock(block, 'outputs')

  const resourcesBlock = parseMapBlock(block, 'resources')

  return {
    type,
    sourcePath,
    deps,
    dependsOn: [],
    command: command.length > 0 ? command : (type === 'command' ? [...DEFAULT_STEP_COMMAND] : []),
    inputs,
    outputs,
    params,
    env,
    cpu: resourcesBlock.cpu ?? '',
    memory: resourcesBlock.memory ?? '',
    gpu: resourcesBlock.gpu ?? '',
  }
}

export function parsePipelineDraftYaml(yaml: string): PipelineDraft {
  const source = yaml || ''
  const nameMatch = source.match(/^\s*name:\s*(.+)$/m)
  const name = nameMatch ? trimQuotes(nameMatch[1]) : 'my-pipeline'
  const steps: PipelineStepDraft[] = []
  const blocks = source.split(/\n(?=\s*-\s+name:)/)

  for (const block of blocks) {
    const stepNameMatch = block.match(/^\s*-\s+name:\s*(.+)$/m)
    if (!stepNameMatch) continue
    const stepName = trimQuotes(stepNameMatch[1]) || nextStepName(steps.length)
    const dependsOn: string[] = []
    const inlineDepends = block.match(/depends_on:\s*\[([^\]]*)]/)
    if (inlineDepends) {
      dependsOn.push(...parseInlineList(inlineDepends[1]))
    } else {
      const dependsBlock = block.match(/depends_on:([\s\S]*?)(?=\n\s+\w|\n\s*-\s+name:|$)/)
      if (dependsBlock) {
        for (const m of dependsBlock[1].matchAll(/^\s*-\s+(.+)$/gm)) {
          const dep = trimQuotes(m[1])
          if (dep) dependsOn.push(dep)
        }
      }
    }

    const parsed = parseStepFields(block)
    steps.push({
      id: nextStepId(),
      name: stepName,
      dependsOn,
      type: parsed.type,
      sourcePath: parsed.sourcePath,
      deps: parsed.deps,
      command: parsed.command,
      inputs: parsed.inputs,
      outputs: parsed.outputs,
      params: parsed.params,
      env: parsed.env,
      cpu: parsed.cpu,
      memory: parsed.memory,
      gpu: parsed.gpu,
    })
  }

  return { name, steps }
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
