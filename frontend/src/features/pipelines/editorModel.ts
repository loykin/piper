// Pure helpers for the pipeline editor page: task defaults, canvas layout,
// git credential auto-match preview, and submit-time validation.
import {
  defaultPipelineStep, validatePipelineDraft,
  type PipelineArtifactDraft, type PipelineDefaultsDraft, type PipelineDraft,
  type PipelineKeyValueDraft, type PipelineStepDraft, type PipelineTaskType,
} from './editor'

// 'none' is a commands-only pipeline: no workspace to snapshot, so Python and
// notebook tasks (which run a file from the source) can't be submitted.
export type SourceKind = 'notebook-volume' | 'git' | 'none'
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

// Palette templates. Piper's executor only runs `prepare` then `command`;
// everything notebook- or Python-specific lives here as visible, editable
// text (docs/pipeline-execution-templates.md). Across runtimes only the
// environment differs: containers use the image's python, baremetal builds a
// venv in the step's work dir and registers its own kernel so papermill never
// picks up a host-level "python3" kernelspec.
// --log-output streams cell output into the step log.
const PAPERMILL_RUN = '--log-output "$PIPER_SCRIPT_PATH" "$PIPER_OUTPUT_DIR/executed.ipynb" -f "$PIPER_PARAMS_FILE"'

export function taskTemplate(type: PipelineTaskType, runtime: string): Pick<PipelineStepDraft, 'prepare' | 'command' | 'outputs'> {
  const venv = runtime === 'baremetal'
  if (type === 'notebook') {
    return venv
      ? {
          prepare: [
            'python3 -m venv .venv',
            '.venv/bin/python -m pip install -q papermill ipykernel',
            '.venv/bin/python -m ipykernel install --prefix .venv --name piper-step',
          ],
          command: ['sh', '-c', `.venv/bin/papermill -k piper-step ${PAPERMILL_RUN}`],
          outputs: [{ name: 'executed-notebook', path: 'executed.ipynb', from: '' }],
        }
      : {
          prepare: ['python3 -m pip install -q papermill ipykernel'],
          command: ['sh', '-c', `papermill ${PAPERMILL_RUN}`],
          outputs: [{ name: 'executed-notebook', path: 'executed.ipynb', from: '' }],
        }
  }
  if (type === 'python') {
    return venv
      ? { prepare: ['python3 -m venv .venv'], command: ['sh', '-c', '.venv/bin/python "$PIPER_SCRIPT_PATH"'], outputs: [] }
      : { prepare: [], command: ['sh', '-c', 'python3 "$PIPER_SCRIPT_PATH"'], outputs: [] }
  }
  return { prepare: [], command: ['echo', 'hello from piper'], outputs: [] }
}

// Prepare presets: buttons that append these lines as visible text. They
// follow the same environment split as the templates (baremetal installs into
// the task's .venv, containers into the image's python) and are offered only
// for notebook and Python tasks, whose templates set that environment up.
export type PreparePreset = 'pip' | 'uv'

/** The workspace file a preset installs from; it must ride in the snapshot. */
export function preparePresetFile(preset: PreparePreset): string {
  return preset === 'pip' ? 'requirements.txt' : 'pyproject.toml'
}

/** The task after applying a preset: its lines appended, its file added to deps. */
export function applyPreparePreset(
  task: Pick<PipelineStepDraft, 'prepare' | 'deps'>, preset: PreparePreset, runtime: string,
): Pick<PipelineStepDraft, 'prepare' | 'deps'> {
  const file = preparePresetFile(preset)
  return {
    prepare: [...task.prepare, ...preparePresetLines(preset, runtime)],
    deps: task.deps.includes(file) ? task.deps : [...task.deps, file],
  }
}

export function preparePresetLines(preset: PreparePreset, runtime: string): string[] {
  const python = runtime === 'baremetal' ? '.venv/bin/python' : 'python3'
  if (preset === 'pip') return [`${python} -m pip install -q -r requirements.txt`]
  const target = runtime === 'baremetal' ? '--python .venv/bin/python' : '--system'
  return [`${python} -m pip install -q uv`, `${python} -m uv pip install ${target} -r pyproject.toml`]
}

/**
 * Visible defaults for the pipeline's environment, filled once and left
 * editable: placement.runtime is the runtime this installation owns, the step image is the image the source notebook already runs
 * (the user picked it there), and the k8s namespace is the server's first
 * allowed one (runtime.namespaces). Values already set are kept.
 */
export function environmentDefaults(
  prev: PipelineDefaultsDraft, runtime: string, sourceImage: string, namespaces: string[],
): PipelineDefaultsDraft {
  const next = { ...prev }
  if (!prev.placementRuntime) next.placementRuntime = runtime
  if (runtime === 'docker' && !prev.dockerImage && sourceImage) next.dockerImage = sourceImage
  if (runtime === 'k8s') {
    if (!prev.k8sImage && sourceImage) next.k8sImage = sourceImage
    if (!prev.k8sNamespace && namespaces[0]) next.k8sNamespace = namespaces[0]
  }
  return next
}

export function defaultTask(type: PipelineTaskType, index = 0, runtime = ''): PipelineStepDraft {
  const draft = defaultPipelineStep(index, type)
  draft.dependsOn = []
  const template = taskTemplate(type, runtime)
  if (type !== 'command') Object.assign(draft, template)
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
