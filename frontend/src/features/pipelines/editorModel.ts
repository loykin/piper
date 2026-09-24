// Pure helpers for the pipeline editor page: task defaults, canvas layout,
// git credential auto-match preview, and submit-time validation.
import {
  defaultPipelineStep, validatePipelineDraft,
  type PipelineArtifactDraft, type PipelineDraft,
  type PipelineKeyValueDraft, type PipelineStepDraft, type PipelineTaskType,
} from './editor'

export type SourceKind = 'notebook-volume' | 'git' | 'local' | 'object-store'
export type ActiveTab = 'design' | 'yaml'

export function designLossMessage(path: string): string {
  return `Design view cannot preserve "${path}". Keep editing or submit from the YAML tab.`
}

export const TASK_LABELS: Record<PipelineTaskType, string> = {
  notebook: 'Notebook Task',
  python: 'Python Task',
  command: 'Command Task',
}


export const SOURCE_LABELS: Record<PipelineTaskType, string> = {
  notebook: 'Notebook file',
  python: 'Script file',
  command: 'Working path',
}

// Shared by the pipeline-level and per-step runtime placement Selects below.
export const RUNTIME_ITEMS = [
  { value: 'baremetal', label: 'Bare metal' },
  { value: 'docker', label: 'Docker' },
  { value: 'k8s', label: 'Kubernetes' },
]

export function buildPositions(tasks: PipelineStepDraft[]): Record<string, { x: number; y: number }> {
  const depth = new Map<string, number>()
  const byName = new Map(tasks.map(t => [t.name, t]))

  const walk = (name: string, seen = new Set<string>()): number => {
    if (depth.has(name)) return depth.get(name) ?? 0
    if (seen.has(name)) return 0
    seen.add(name)
    const task = byName.get(name)
    if (!task || task.dependsOn.length === 0) { depth.set(name, 0); return 0 }
    const value = Math.max(...task.dependsOn.map(dep => walk(dep, seen) + 1))
    depth.set(name, value)
    return value
  }

  for (const task of tasks) walk(task.name)

  const columns = new Map<number, string[]>()
  for (const [name, value] of depth) {
    const list = columns.get(value) ?? []
    list.push(name)
    columns.set(value, list)
  }

  const NODE_W = 248, NODE_H = 96, GAP_X = 96, GAP_Y = 28
  const positions: Record<string, { x: number; y: number }> = {}

  for (const [col, names] of columns) {
    names.forEach((name, row) => {
      const task = byName.get(name)
      if (task) positions[task.id] = { x: col * (NODE_W + GAP_X) + 24, y: row * (NODE_H + GAP_Y) + 24 }
    })
  }

  if (Object.keys(positions).length === 0) {
    tasks.forEach((task, i) => {
      positions[task.id] = { x: 24 + (i % 3) * 300, y: 24 + Math.floor(i / 3) * 140 }
    })
  }

  return positions
}

export function emptyPair(): PipelineKeyValueDraft { return { key: '', value: '' } }
export function emptyArtifact(): PipelineArtifactDraft { return { name: '', path: '', from: '' } }

// Mirrors the backend credential endpoint scope check (scheme + host match,
// then path-boundary prefix). Used only to preview which credential the backend
// will auto-select for a repo URL — the actual match happens server-side.
export function endpointInScope(endpoint: string, repoURL: string): boolean {
  try {
    const ep = new URL(endpoint)
    const repo = new URL(repoURL)
    if (ep.protocol !== repo.protocol || ep.host !== repo.host) return false
    const epPath = ep.pathname.endsWith('/') ? ep.pathname : `${ep.pathname}/`
    const repoPath = repo.pathname.endsWith('/') ? repo.pathname : `${repo.pathname}/`
    return repoPath.startsWith(epPath)
  } catch {
    return false
  }
}

// Picks the most specific (longest endpoint path) git credential whose scope
// contains repoURL, mirroring the backend auto-match behavior.
export function autoMatchGitCredential<T extends { endpoint?: string }>(credentials: T[], repoURL: string): T | undefined {
  if (!repoURL.trim()) return undefined
  return credentials
    .filter(c => c.endpoint && endpointInScope(c.endpoint, repoURL))
    .sort((a, b) => (b.endpoint?.length ?? 0) - (a.endpoint?.length ?? 0))[0]
}

export function defaultTask(type: PipelineTaskType, index = 0): PipelineStepDraft {
  const draft = defaultPipelineStep(index, type)
  draft.dependsOn = []
  if (type === 'python') draft.command = ['python', 'task.py']
  else if (type === 'notebook') draft.command = []
  return draft
}

/**
 * Every problem that blocks submitting `draft` on a server that owns
 * `serverRuntime`, runtime-placement mismatch first. The Design tab, the YAML
 * tab, Validate, and "Apply YAML to Graph" all check exactly this list.
 */
export function draftProblems(draft: PipelineDraft, serverRuntime: string): string[] {
  const messages = validatePipelineDraft(draft)
  const placement = draft.defaults.placementRuntime
  if (serverRuntime && placement && placement !== serverRuntime) {
    messages.unshift(`This Piper installation owns the ${serverRuntime} runtime; placement.runtime must be ${serverRuntime} or empty.`)
  }
  return messages
}
