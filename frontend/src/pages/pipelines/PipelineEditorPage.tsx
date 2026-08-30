import { useEffect, useMemo, useRef, useState, type DragEvent } from 'react'
import { useNavigate, useSearchParams } from '@/lib/router'
import {
  ArrowDown, ArrowUp,
  Code2, FileCode2, FolderOpen, HardDrive, Plus, BookOpen, Trash2, Upload, X,
} from 'lucide-react'
import {
  DataBodyTemplate, FormActions, FormField, PageTopBar, WorkbenchBodyTemplate, Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
  Tabs, TabsContent, TabsList, TabsTrigger,
} from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ShellMirror } from '@/components/ui/shell-mirror'
import { YamlMirror } from '@/components/ui/yaml-mirror'
import { IconButton } from '@/components/ui/icon-button'
import PipelineCanvas from '@/shared/components/PipelineCanvas'
import { EnvVarEditor } from '@/shared/components/EnvVarEditor'
import { emptyEnvVarDraft, type EnvVarDraft } from '@/shared/env'

import { listNotebookVolumes, listVolumeFiles, type NotebookVolume } from '@/features/notebooks/api'
import { useCredentials } from '@/features/credentials/hooks'
import { getPipeline } from '@/features/pipelines/api'
import { useCreatePipeline } from '@/features/pipelines/hooks'
import { useSystemSettings } from '@/features/system/hooks'
import { useProjectId } from '@/lib/projectContext'
import {
  buildPipelineDraftYaml, defaultPipelineDraft, defaultPipelineStep,
  findPipelineDraftYamlDifference, parsePipelineDraftYaml, stripMetadataVersion, validatePipelineDraft,
  type PipelineSourceDraft,
  type PipelineArtifactDraft, type PipelineKeyValueDraft,
  type PipelineDefaultsDraft, type PipelineStepDraft, type PipelineTaskType,
} from '@/features/pipelines/editor'

type SourceKind = 'notebook-volume' | 'git' | 'local' | 'object-store'
type ActiveTab = 'design' | 'yaml'

function designLossMessage(path: string): string {
  return `Design view cannot preserve "${path}". Keep editing or submit from the YAML tab.`
}

const TASK_LABELS: Record<PipelineTaskType, string> = {
  notebook: 'Notebook Task',
  python: 'Python Task',
  command: 'Command Task',
}

const TASK_ICONS: Record<PipelineTaskType, React.ReactNode> = {
  notebook: <BookOpen size={16} className="text-muted-foreground" />,
  python: <Code2 size={16} className="text-muted-foreground" />,
  command: <FileCode2 size={16} className="text-muted-foreground" />,
}

const SOURCE_LABELS: Record<PipelineTaskType, string> = {
  notebook: 'Notebook file',
  python: 'Script file',
  command: 'Working path',
}

function buildPositions(tasks: PipelineStepDraft[]): Record<string, { x: number; y: number }> {
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

function emptyPair(): PipelineKeyValueDraft { return { key: '', value: '' } }
function emptyArtifact(): PipelineArtifactDraft { return { name: '', path: '', from: '' } }

// Mirrors the backend credential endpoint scope check (scheme + host match,
// then path-boundary prefix). Used only to preview which credential the backend
// will auto-select for a repo URL — the actual match happens server-side.
function endpointInScope(endpoint: string, repoURL: string): boolean {
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
function autoMatchGitCredential<T extends { endpoint?: string }>(credentials: T[], repoURL: string): T | undefined {
  if (!repoURL.trim()) return undefined
  return credentials
    .filter(c => c.endpoint && endpointInScope(c.endpoint, repoURL))
    .sort((a, b) => (b.endpoint?.length ?? 0) - (a.endpoint?.length ?? 0))[0]
}

function defaultTask(type: PipelineTaskType, index = 0): PipelineStepDraft {
  const draft = defaultPipelineStep(index, type)
  draft.dependsOn = []
  if (type === 'python') draft.command = ['python', 'task.py']
  else if (type === 'notebook') draft.command = []
  return draft
}

// --- sub-components ---

interface FileBrowseDropdownProps {
  files: string[]
  ext?: string
  query: string
  onQueryChange: (q: string) => void
  onSelect: (file: string) => void
}

function FileBrowseDropdown({ files, ext, query, onQueryChange, onSelect }: FileBrowseDropdownProps) {
  const q = query.toLowerCase()
  const filtered = files.filter(f => f.toLowerCase().includes(q))
  return (
    <div className="absolute left-0 right-0 top-full z-50 mt-1 overflow-hidden rounded-lg border border-border bg-card shadow-lg">
      <div className="border-b border-border px-2 py-2">
        <Input
          autoFocus
          className="h-7 text-xs"
          placeholder={ext ? `Search ${ext} files…` : 'Search files…'}
          value={query}
          onChange={e => onQueryChange(e.target.value)}
        />
      </div>
      {filtered.length === 0 ? (
        <p className="px-3 py-3 text-xs text-muted-foreground">No files found.</p>
      ) : (
        <div className="max-h-48 overflow-y-auto">
          {filtered.map(f => (
            <Button
              key={f}
              type="button"
              variant="ghost"
              size="sm"
              className="w-full justify-start rounded-none px-3 font-normal"
              onClick={() => onSelect(f)}
            >
              {ext && <span className="font-mono text-muted-foreground">{ext}</span>}
              <span className="truncate font-mono">{f}</span>
            </Button>
          ))}
        </div>
      )}
    </div>
  )
}

interface DepBrowseDropdownProps {
  files: string[]
  query: string
  onQueryChange: (q: string) => void
  onSelect: (path: string) => void
}

function DepBrowseDropdown({ files, query, onQueryChange, onSelect }: DepBrowseDropdownProps) {
  const dirs = [...new Set(
    files.flatMap(f => {
      const parts = f.split('/')
      const result: string[] = []
      for (let i = 1; i < parts.length; i++) {
        result.push(parts.slice(0, i).join('/') + '/')
      }
      return result
    })
  )].sort()

  const q = query.toLowerCase()
  const filteredDirs = dirs.filter(d => d.toLowerCase().includes(q))
  const filteredFiles = files.filter(f => f.toLowerCase().includes(q))
  const empty = filteredDirs.length === 0 && filteredFiles.length === 0

  return (
    <div className="absolute left-0 right-0 top-full z-50 mt-1 overflow-hidden rounded-lg border border-border bg-card shadow-lg">
      <div className="border-b border-border px-2 py-2">
        <Input
          autoFocus
          className="h-7 text-xs"
          placeholder="Search files and directories…"
          value={query}
          onChange={e => onQueryChange(e.target.value)}
        />
      </div>
      {empty ? (
        <p className="px-3 py-3 text-xs text-muted-foreground">No results.</p>
      ) : (
        <div className="max-h-52 overflow-y-auto">
          {filteredDirs.length > 0 && (
            <>
              <p className="px-3 pt-2 text-[10px] uppercase tracking-wider text-muted-foreground">Directories</p>
              {filteredDirs.map(d => (
                <Button key={d} type="button" variant="ghost" size="sm"
                  className="w-full justify-start rounded-none px-3 font-normal"
                  onClick={() => onSelect(d)}
                >
                  <span className="shrink-0 text-muted-foreground">📁</span>
                  <span className="truncate font-mono">{d}</span>
                </Button>
              ))}
            </>
          )}
          {filteredFiles.length > 0 && (
            <>
              <p className="px-3 pt-2 text-[10px] uppercase tracking-wider text-muted-foreground">Files</p>
              {filteredFiles.map(f => (
                <Button key={f} type="button" variant="ghost" size="sm"
                  className="w-full justify-start rounded-none px-3 font-normal"
                  onClick={() => onSelect(f)}
                >
                  <span className="shrink-0 text-muted-foreground">📄</span>
                  <span className="truncate font-mono">{f}</span>
                </Button>
              ))}
            </>
          )}
        </div>
      )}
    </div>
  )
}

interface PairSectionProps {
  label: string
  emptyText: string
  keyPlaceholder?: string
  valuePlaceholder?: string
  items: PipelineKeyValueDraft[]
  onAdd: () => void
  onRemove: (rowIndex: number) => void
  onUpdate: (rowIndex: number, patch: Partial<PipelineKeyValueDraft>) => void
}

function PairSection({ label, emptyText, keyPlaceholder = 'key', valuePlaceholder = 'value', items, onAdd, onRemove, onUpdate }: PairSectionProps) {
  return (
    <div>
      <div className="mb-2 flex items-center justify-between">
        <label className="block text-[11px] uppercase tracking-wider text-muted-foreground">{label}</label>
        <Button variant="outline" size="sm" onClick={onAdd}><Plus size={14} className="mr-1.5" /> Add</Button>
      </div>
      <div className="space-y-2">
        {items.length === 0 ? (
          <p className="text-xs text-muted-foreground">{emptyText}</p>
        ) : items.map((item, i) => (
          <div key={i} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] gap-2">
            <Input value={item.key} placeholder={keyPlaceholder} onChange={e => onUpdate(i, { key: e.target.value })} />
            <Input value={item.value} placeholder={valuePlaceholder} onChange={e => onUpdate(i, { value: e.target.value })} />
            <IconButton icon={<Trash2 />} label="Remove" onClick={() => onRemove(i)} className="text-destructive hover:bg-destructive/10" />
          </div>
        ))}
      </div>
    </div>
  )
}

interface ArtifactSectionProps {
  label: string
  kind: 'inputs' | 'outputs'
  items: PipelineArtifactDraft[]
  canBrowse: boolean
  volumeFiles: string[]
  activeBrowseKey: string | null
  browseQuery: string
  browseRef: React.RefObject<HTMLDivElement | null>
  onAdd: () => void
  onRemove: (rowIndex: number) => void
  onUpdate: (rowIndex: number, patch: Partial<PipelineArtifactDraft>) => void
  onBrowseToggle: (key: string) => void
  onBrowseQueryChange: (q: string) => void
  onBrowseSelect: (rowIndex: number, file: string) => void
}

function ArtifactSection({
  label, kind, items, canBrowse, volumeFiles, activeBrowseKey, browseQuery, browseRef,
  onAdd, onRemove, onUpdate, onBrowseToggle, onBrowseQueryChange, onBrowseSelect,
}: ArtifactSectionProps) {
  return (
    <div>
      <div className="mb-2 flex items-center justify-between">
        <label className="block text-[11px] uppercase tracking-wider text-muted-foreground">{label}</label>
        <Button variant="outline" size="sm" onClick={onAdd}><Plus size={14} className="mr-1.5" /> Add</Button>
      </div>
      <div className="space-y-2">
        {items.length === 0 ? (
          <p className="text-xs text-muted-foreground">No {label.toLowerCase()}.</p>
        ) : items.map((item, rowIndex) => {
          const browseKey = `${kind}-${rowIndex}`
          const isBrowseOpen = activeBrowseKey === browseKey
          return (
            <div key={rowIndex} className="grid gap-1">
              <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2">
                <Input value={item.name} placeholder="name" onChange={e => onUpdate(rowIndex, { name: e.target.value })} />
                <IconButton icon={<Trash2 />} label="Remove" onClick={() => onRemove(rowIndex)} className="text-destructive hover:bg-destructive/10" />
              </div>
              <div ref={isBrowseOpen ? browseRef : null} className="relative">
                <div className="flex gap-1.5">
                  <Input value={item.path} placeholder="path in workspace" onChange={e => onUpdate(rowIndex, { path: e.target.value })} />
                  {canBrowse && (
                    <IconButton
                      icon={<FolderOpen />}
                      label="Browse volume files"
                      onClick={() => onBrowseToggle(browseKey)}
                    />
                  )}
                </div>
                {isBrowseOpen && (
                  <FileBrowseDropdown
                    files={volumeFiles}
                    query={browseQuery}
                    onQueryChange={onBrowseQueryChange}
                    onSelect={f => onBrowseSelect(rowIndex, f)}
                  />
                )}
              </div>
              <Input value={item.from} placeholder="from (task name)" onChange={e => onUpdate(rowIndex, { from: e.target.value })} />
            </div>
          )
        })}
      </div>
    </div>
  )
}

// --- page ---

export default function PipelineEditorPage() {
  const navigate = useNavigate()
  const projectId = useProjectId()
  const { mutateAsync: createPipeline } = useCreatePipeline()
  const initialDraft = useMemo(() => defaultPipelineDraft(), [])

  const [searchParams, setSearchParams] = useSearchParams()

  // URL is the source of truth for setup choices
  const setupDone = useMemo(() => {
    const s = searchParams.get('source')
    if (!s) return false
    if (s === 'notebook-volume') return !!searchParams.get('volume')
    if (s === 'git') return !!searchParams.get('repo')
    return !!searchParams.get('root')
  }, [searchParams])

  const editorSourceKind = (searchParams.get('source') as SourceKind) ?? 'notebook-volume'
  const editorVolumeId  = searchParams.get('volume') ?? ''
  const editorRoot      = searchParams.get('root')   ?? ''
  const editorCredential = searchParams.get('credential') ?? ''
  const editorRepo      = searchParams.get('repo') ?? ''
  const editorBranch    = searchParams.get('branch') ?? ''
  const editorName      = searchParams.get('name')   ?? initialDraft.name
  const editorFromVersion = searchParams.get('from_version') ?? ''

  // Setup form — local state before user commits to URL
  const [formName,       setFormName]       = useState(editorName)
  const [formSourceKind, setFormSourceKind] = useState<SourceKind>(editorSourceKind)
  const [formVolumeId,   setFormVolumeId]   = useState(editorVolumeId)
  const [formRoot,       setFormRoot]       = useState(editorRoot)
  const [formCredential, setFormCredential] = useState(editorCredential)
  const [formRepo,       setFormRepo]       = useState(editorRepo)
  const [formBranch,     setFormBranch]     = useState(editorBranch)

  const [activeTab, setActiveTab] = useState<ActiveTab>('design')
  const [pipelineName, setPipelineName] = useState(editorName)
  const [volumes, setVolumes] = useState<NotebookVolume[]>([])
  const [volumeFiles, setVolumeFiles] = useState<string[]>([])
  const [volumeFilesStatus, setVolumeFilesStatus] = useState<'ready' | 'transitioning' | 'unavailable' | null>(null)
  const [tasks, setTasks] = useState<PipelineStepDraft[]>(initialDraft.steps)
  const [defaults, setDefaults] = useState<PipelineDefaultsDraft>(initialDraft.defaults)

  // This Piper installation owns exactly one runtime (baremetal, docker, or
  // k8s) for direct in-process execution — prefill it once as the pipeline
  // default so placement.runtime always matches what the server can run.
  const { data: systemSettings, isLoading: systemSettingsLoading } = useSystemSettings()
  const serverRuntime = systemSettings?.runtime?.type ?? ''
  const runtimeAutofilledRef = useRef(false)
  useEffect(() => {
    if (runtimeAutofilledRef.current) return
    if (systemSettingsLoading || !serverRuntime) return
    runtimeAutofilledRef.current = true
    if (!defaults.placementRuntime) {
      setDefaults(prev => (prev.placementRuntime ? prev : { ...prev, placementRuntime: serverRuntime }))
    }
  }, [defaults.placementRuntime, serverRuntime, systemSettingsLoading])
  const [positions, setPositions] = useState<Record<string, { x: number; y: number }>>(() => buildPositions(initialDraft.steps))
  const [selectedId, setSelectedId] = useState<string>(initialDraft.steps[0]?.id ?? '')
  const [editingId, setEditingId] = useState<string | null>(null)
  const [fileBrowserOpen, setFileBrowserOpen] = useState(false)
  const fileBrowserRef = useRef<HTMLDivElement>(null)
  const [artifactBrowseKey, setArtifactBrowseKey] = useState<string | null>(null)
  const artifactBrowseRef = useRef<HTMLDivElement>(null)
  const [browseQuery, setBrowseQuery] = useState('')
  const [yamlText, setYamlText] = useState(() => buildPipelineDraftYaml(initialDraft))
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [submitModalOpen, setSubmitModalOpen] = useState(false)
  // Resolve the exact YAML at Submit time. Keeping it in a ref guarantees the
  // confirmation action sends the same lossless document the user reviewed,
  // even if other editor state changes while the modal is open.
  const pendingSubmitRef = useRef<{ name: string; yaml: string } | null>(null)
  const [submitVolumeId, setSubmitVolumeId] = useState('')
  const [resetKey, setResetKey] = useState(0)
  const draggingTaskTypeRef = useRef<PipelineTaskType | null>(null)
  const dragDropHandledRef = useRef(false)
  const { data: credentials = [] } = useCredentials()
  // Keep the pipeline's current credential in the list even if it has since
  // been disabled, so it doesn't render as if the reference were lost.
  const gitCredentials = credentials.filter(credential => credential.kind === 'git' && (!credential.disabled || credential.name === formCredential))
  // When no credential is explicitly chosen, preview which one the backend will
  // auto-match by endpoint scope so the user knows the source is authenticated.
  const autoMatchedCredential = useMemo(
    () => (formCredential ? undefined : autoMatchGitCredential(gitCredentials, formRepo)),
    [formCredential, gitCredentials, formRepo],
  )
  const pipelineSource = useMemo<PipelineSourceDraft | undefined>(() => {
    if (editorSourceKind !== 'git') return undefined
    return {
      type: 'git',
      repo: editorRepo,
      branch: editorBranch,
      credentialRef: editorCredential,
    }
  }, [editorSourceKind, editorRepo, editorBranch, editorCredential])

  useEffect(() => {
    if (!projectId) return
    listNotebookVolumes(projectId).then(setVolumes).catch(() => setVolumes([]))
  }, [projectId])

  useEffect(() => {
    if (!projectId || !editorFromVersion) return
    let cancelled = false
    getPipeline(projectId, editorFromVersion).then(template => {
      if (cancelled) return
      // Preserve the fetched document before parsing. Even if the Design
      // parser cannot understand a valid server-side feature, the user can
      // still inspect, edit, and resubmit the exact stored YAML.
      // Strip the server-stamped metadata.version: submitting it unchanged
      // would collide with the version that already exists (ErrVersionExists)
      // instead of letting the server auto-assign the next one, the same way
      // a brand-new template submission does.
      const yaml = stripMetadataVersion(template.yaml)
      setPipelineName(template.name)
      setFormName(template.name)
      setYamlText(yaml)
      setActiveTab('yaml')
      const parsed = parsePipelineDraftYaml(yaml)
      const designDraft = { ...parsed, source: pipelineSource }
      const difference = findPipelineDraftYamlDifference(yaml, designDraft)
      setTasks(parsed.steps)
      setDefaults(parsed.defaults)
      setPositions(buildPositions(parsed.steps))
      setSelectedId(parsed.steps[0]?.id ?? '')
      setEditingId(null)
      if (difference) {
        // A previous version may contain valid manifest fields that the visual
        // editor does not model. Open the lossless view and retain the exact
        // stored YAML instead of silently generating a reduced document.
        setError(designLossMessage(difference))
      } else {
        setYamlText(buildPipelineDraftYaml(designDraft))
        setActiveTab('design')
        setError('')
      }
    }).catch(err => {
      if (!cancelled) setError(err instanceof Error ? err.message : String(err))
    })
    return () => { cancelled = true }
  }, [projectId, editorFromVersion, pipelineSource])

  useEffect(() => {
    if (!fileBrowserOpen && !artifactBrowseKey) return
    const handler = (e: MouseEvent) => {
      if (fileBrowserRef.current && !fileBrowserRef.current.contains(e.target as Node)) setFileBrowserOpen(false)
      if (artifactBrowseRef.current && !artifactBrowseRef.current.contains(e.target as Node)) setArtifactBrowseKey(null)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [fileBrowserOpen, artifactBrowseKey])

  useEffect(() => {
    if (editorSourceKind !== 'notebook-volume' || !editorVolumeId) {
      setVolumeFiles([])
      setVolumeFilesStatus(null)
      return
    }
    let retryTimer: ReturnType<typeof setTimeout> | null = null
    function fetchFiles() {
      listVolumeFiles(projectId, editorVolumeId!).then(result => {
        if (result.state === 'transitioning') {
          setVolumeFilesStatus('transitioning')
          // Keep existing files during transition — do not clear to empty.
          retryTimer = setTimeout(fetchFiles, result.retryAfterMs || 2000)
        } else {
          setVolumeFiles(result.files)
          setVolumeFilesStatus(result.state)
        }
      }).catch(() => {
        setVolumeFiles([])
        setVolumeFilesStatus('unavailable')
      })
    }
    fetchFiles()
    return () => { if (retryTimer != null) clearTimeout(retryTimer) }
  }, [editorSourceKind, editorVolumeId, projectId])

  useEffect(() => {
    // Only the Design tab drives yamlText. While the YAML tab is active, its
    // textarea is the user's working draft — regenerating from Design state
    // here (e.g. because the runtime-autofill effect above just nudged
    // `defaults`) would silently clobber whatever they're mid-typing.
    if (activeTab !== 'yaml') {
      setYamlText(buildPipelineDraftYaml({ name: pipelineName, steps: tasks, source: pipelineSource, defaults }))
    }
    if (!tasks.some(t => t.id === selectedId)) setSelectedId(tasks[0]?.id ?? '')
    if (editingId && !tasks.some(t => t.id === editingId)) setEditingId(null)
  }, [pipelineName, tasks, pipelineSource, defaults, selectedId, editingId, activeTab])

  useEffect(() => {
    setPositions(prev => {
      const next = { ...prev }
      const known = new Set(tasks.map(t => t.id))
      for (const task of tasks) {
        if (!next[task.id]) next[task.id] = buildPositions(tasks)[task.id] ?? { x: 24, y: 24 }
      }
      for (const id of Object.keys(next)) {
        if (!known.has(id)) delete next[id]
      }
      return next
    })
  }, [tasks])

  const editingIndex = useMemo(() => tasks.findIndex(t => t.id === editingId), [tasks, editingId])
  const editingTask = editingIndex >= 0 ? tasks[editingIndex] : null
  const selectedVolume = useMemo(() => volumes.find(v => v.id === editorVolumeId) ?? null, [editorVolumeId, volumes])
  const canBrowse = editorSourceKind === 'notebook-volume'
  const yamlStatus = useMemo(() => {
    try {
      const parsed = parsePipelineDraftYaml(yamlText)
      const difference = findPipelineDraftYamlDifference(yamlText, { ...parsed, source: pipelineSource })
      return { parseError: '', difference }
    } catch (err) {
      return { parseError: err instanceof Error ? err.message : String(err), difference: null }
    }
  }, [pipelineSource, yamlText])

  function updateTask(index: number, patch: Partial<PipelineStepDraft>) {
    setTasks(current => {
      const next = current.map((task, i) => i !== index ? task : { ...task, ...patch })
      const prev = current[index]
      const nextName = patch.name?.trim()
      if (prev && nextName && nextName !== prev.name) {
        next.forEach((task, i) => {
          if (i !== index) task.dependsOn = task.dependsOn.map(dep => dep === prev.name ? nextName : dep)
        })
      }
      return next
    })
  }

  function updateDefaults(patch: Partial<PipelineDefaultsDraft>) {
    setDefaults(current => ({ ...current, ...patch }))
  }

  function updateTaskDriver(index: number, patch: Partial<PipelineStepDraft['driver']>) {
    setTasks(current => current.map((task, i) => (
      i === index ? { ...task, driver: { ...task.driver, ...patch } } : task
    )))
  }

  function updateDep(index: number, depIndex: number, val: string) {
    setTasks(current => current.map((task, i) => {
      if (i !== index) return task
      const deps = [...task.deps]
      deps[depIndex] = val
      return { ...task, deps }
    }))
  }

  function addDep(index: number) {
    setTasks(current => current.map((task, i) =>
      i !== index ? task : { ...task, deps: [...task.deps, ''] }
    ))
  }

  function removeDep(index: number, depIndex: number) {
    setTasks(current => current.map((task, i) =>
      i !== index ? task : { ...task, deps: task.deps.filter((_, j) => j !== depIndex) }
    ))
  }

  function addTask(type: PipelineTaskType = 'command', position?: { x: number; y: number }) {
    setTasks(current => {
      const next = [...current, defaultTask(type, current.length)]
      const created = next[next.length - 1]
      setSelectedId(created.id)
      if (position) setPositions(pos => ({ ...pos, [created.id]: position }))
      return next
    })
  }

  function removeTask(index: number) {
    setTasks(current => {
      const removed = current[index]
      const next = current.filter((_, i) => i !== index).map(task => ({
        ...task,
        dependsOn: task.dependsOn.filter(dep => dep !== removed?.name),
      }))
      if (removed) setPositions(pos => { const p = { ...pos }; delete p[removed.id]; return p })
      if (selectedId === removed?.id) setSelectedId(next[0]?.id ?? '')
      if (editingId === removed?.id) setEditingId(null)
      return next
    })
  }

  function moveTask(index: number, delta: -1 | 1) {
    setTasks(current => {
      const target = index + delta
      if (target < 0 || target >= current.length) return current
      const next = [...current]
      const [item] = next.splice(index, 1)
      next.splice(target, 0, item)
      return next
    })
  }

  function connectTasks(sourceId: string, targetId: string) {
    setTasks(current => {
      const source = current.find(t => t.id === sourceId)
      const targetIndex = current.findIndex(t => t.id === targetId)
      if (!source || targetIndex < 0 || source.id === targetId) return current
      setSelectedId(targetId)
      return current.map(task => task.id !== targetId ? task : {
        ...task,
        dependsOn: Array.from(new Set([...task.dependsOn, source.name])),
      })
    })
  }

  function disconnectTasks(sourceId: string, targetId: string) {
    setTasks(current => {
      const source = current.find(t => t.id === sourceId)
      if (!source) return current
      return current.map(task => task.id !== targetId ? task : {
        ...task,
        dependsOn: task.dependsOn.filter(dep => dep !== source.name),
      })
    })
  }

  function resetLayout() { setPositions(buildPositions(tasks)); setResetKey(k => k + 1) }
  function openTaskEditor(id: string) { setSelectedId(id); setEditingId(id) }

  function applyYamlToDesign(): boolean {
    try {
      const parsed = parsePipelineDraftYaml(yamlText)
      const designDraft = { ...parsed, source: pipelineSource }
      const difference = findPipelineDraftYamlDifference(yamlText, designDraft)
      if (difference) {
        setError(designLossMessage(difference))
        return false
      }
      const messages = validatePipelineDraft(designDraft)
      const runtimeMessage = validateRuntimePlacement(designDraft.defaults)
      if (runtimeMessage) messages.unshift(runtimeMessage)
      if (messages.length > 0) {
        setError(messages[0])
        return false
      }
      setPipelineName(designDraft.name)
      setTasks(designDraft.steps)
      setDefaults(designDraft.defaults)
      setPositions(buildPositions(designDraft.steps))
      setSelectedId(designDraft.steps[0]?.id ?? '')
      setEditingId(null)
      setYamlText(buildPipelineDraftYaml(designDraft))
      setError('')
      setActiveTab('design')
      return true
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      return false
    }
  }

  function handleTabChange(nextTab: ActiveTab) {
    if (nextTab === activeTab) return
    if (activeTab === 'yaml' && nextTab === 'design') {
      applyYamlToDesign()
      return
    }
    setError('')
    setActiveTab(nextTab)
  }

  function resolveSubmitPayload(): { name: string; yaml: string } | null {
    if (activeTab !== 'yaml') {
      const draft = { name: pipelineName, steps: tasks, source: pipelineSource, defaults }
      const runtimeMessage = validateRuntimePlacement(defaults)
      if (runtimeMessage) {
        setError(runtimeMessage)
        return null
      }
      const messages = validatePipelineDraft(draft)
      if (messages.length > 0) {
        setError(messages[0])
        return null
      }
      return { name: pipelineName, yaml: buildPipelineDraftYaml(draft) }
    }

    try {
      const parsed = parsePipelineDraftYaml(yamlText)
      // YAML is the authoritative document in this tab. Do not round-trip it
      // through the intentionally smaller Design model: the server's strict
      // manifest decoder must see the exact fields the user entered.
      return { name: parsed.name, yaml: yamlText }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      return null
    }
  }

  async function handleSubmit() {
    const payload = resolveSubmitPayload()
    if (!payload) return
    setError('')
    pendingSubmitRef.current = payload
    setSubmitModalOpen(true)
    setSubmitVolumeId(editorSourceKind === 'notebook-volume' ? editorVolumeId : '')
  }

  async function confirmSubmit() {
    const pending = pendingSubmitRef.current ?? {
      name: pipelineName,
      yaml: buildPipelineDraftYaml({ name: pipelineName, steps: tasks, source: pipelineSource, defaults }),
    }
    setSubmitting(true)
    setError('')
    try {
      await createPipeline({
        yaml: pending.yaml,
        volume_id: submitVolumeId || undefined,
      })
      setSubmitModalOpen(false)
      navigate(`/projects/${projectId}/pipelines?name=${encodeURIComponent(pending.name)}`)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSubmitting(false)
    }
  }

  function closeSubmitModal() {
    if (submitting) return
    setSubmitModalOpen(false)
    pendingSubmitRef.current = null
    setError('')
  }

  function validateNow() {
    if (activeTab === 'yaml') {
      if (yamlStatus.parseError) {
        setError(yamlStatus.parseError)
      } else if (yamlStatus.difference) {
        setError(designLossMessage(yamlStatus.difference))
      } else {
        setError('')
      }
      return
    }
    const messages = validatePipelineDraft({ name: pipelineName, steps: tasks, source: pipelineSource, defaults })
    const runtimeMessage = validateRuntimePlacement(defaults)
    if (runtimeMessage) messages.unshift(runtimeMessage)
    setError(messages[0] || '')
  }

  function validateRuntimePlacement(candidate: PipelineDefaultsDraft): string {
    if (!serverRuntime) return ''
    if (candidate.placementRuntime && candidate.placementRuntime !== serverRuntime) {
      return `This Piper installation owns the ${serverRuntime} runtime; placement.runtime must be ${serverRuntime} or empty.`
    }
    return ''
  }

  function handlePaletteDragStart(event: DragEvent<HTMLButtonElement>, type: PipelineTaskType) {
    draggingTaskTypeRef.current = type
    dragDropHandledRef.current = false
    event.dataTransfer.setData('application/x-piper-step', type)
    event.dataTransfer.setData('text/plain', type)
    event.dataTransfer.effectAllowed = 'copy'
  }

  function handlePaletteDragEnd() {
    const type = draggingTaskTypeRef.current
    draggingTaskTypeRef.current = null
    if (!dragDropHandledRef.current && type) addTask(type)
    dragDropHandledRef.current = false
  }

  function updateArtifactField(index: number, kind: 'inputs' | 'outputs', rowIndex: number, patch: Partial<PipelineArtifactDraft>) {
    setTasks(current => current.map((task, i) => {
      if (i !== index) return task
      const items = [...task[kind]]
      items[rowIndex] = { ...items[rowIndex], ...patch }
      return { ...task, [kind]: items } as PipelineStepDraft
    }))
  }

  function updateParamField(index: number, rowIndex: number, patch: Partial<PipelineKeyValueDraft>) {
    setTasks(current => current.map((task, i) => {
      if (i !== index) return task
      const items = [...task.params]
      items[rowIndex] = { ...items[rowIndex], ...patch }
      return { ...task, params: items } as PipelineStepDraft
    }))
  }

  function updateEnvField(index: number, rowIndex: number, patch: Partial<EnvVarDraft>) {
    setTasks(current => current.map((task, i) => {
      if (i !== index) return task
      const env = [...task.env]
      env[rowIndex] = { ...env[rowIndex], ...patch }
      return { ...task, env } as PipelineStepDraft
    }))
  }

  function addArtifactRow(index: number, kind: 'inputs' | 'outputs') {
    setTasks(current => current.map((task, i) =>
      i !== index ? task : { ...task, [kind]: [...task[kind], emptyArtifact()] } as PipelineStepDraft
    ))
  }

  function removeArtifactRow(index: number, kind: 'inputs' | 'outputs', rowIndex: number) {
    setTasks(current => current.map((task, i) =>
      i !== index ? task : { ...task, [kind]: task[kind].filter((_, j) => j !== rowIndex) } as PipelineStepDraft
    ))
  }

  function addParamRow(index: number) {
    setTasks(current => current.map((task, i) =>
      i !== index ? task : { ...task, params: [...task.params, emptyPair()] } as PipelineStepDraft
    ))
  }

  function addEnvRow(index: number) {
    setTasks(current => current.map((task, i) =>
      i !== index ? task : { ...task, env: [...task.env, emptyEnvVarDraft()] } as PipelineStepDraft
    ))
  }

  function removeParamRow(index: number, rowIndex: number) {
    setTasks(current => current.map((task, i) =>
      i !== index ? task : { ...task, params: task.params.filter((_, j) => j !== rowIndex) } as PipelineStepDraft
    ))
  }

  function removeEnvRow(index: number, rowIndex: number) {
    setTasks(current => current.map((task, i) =>
      i !== index ? task : { ...task, env: task.env.filter((_, j) => j !== rowIndex) } as PipelineStepDraft
    ))
  }

  const canProceed = formSourceKind === 'notebook-volume'
    ? formVolumeId !== ''
    : formSourceKind === 'git'
      ? formRepo.trim() !== ''
      : formRoot.trim() !== ''

  function handleStartEditing() {
    const name = formName.trim() || 'my-pipeline'
    const params: Record<string, string> = { source: formSourceKind, name }
    if (formSourceKind === 'notebook-volume') params.volume = formVolumeId
    else if (formSourceKind === 'git') {
      if (formCredential.trim()) params.credential = formCredential.trim()
      params.repo = formRepo.trim()
      if (formBranch.trim()) params.branch = formBranch.trim()
    }
    else params.root = formRoot
    setPipelineName(name)
    setSearchParams(params, { replace: true })
  }

  function handleGitCredentialChange(name: string) {
    setFormCredential(name)
    const credential = gitCredentials.find(item => item.name === name)
    if (credential?.endpoint && !formRepo.trim()) setFormRepo(credential.endpoint)
  }

  if (!setupDone) {
    return (
      <DataBodyTemplate
        topBar={<PageTopBar left="Pipeline Templates / New Pipeline" />}
        title="New Pipeline"
        description="A pipeline uses exactly one source workspace. Lock it in before you start editing."
      >
        <DataBodyTemplate.Group layout="stacked">
          <form
            className="space-y-3"
            onSubmit={e => {
              e.preventDefault()
              handleStartEditing()
            }}
          >
            <FormField label="Pipeline Name" htmlFor="pipeline-name">
              <Input id="pipeline-name" value={formName} onChange={e => setFormName(e.target.value)} />
            </FormField>
            <FormField label="Source Type" htmlFor="pipeline-source-type">
              <Select value={formSourceKind} onValueChange={v => setFormSourceKind(v as SourceKind)}>
                <SelectTrigger id="pipeline-source-type"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="notebook-volume">Notebook Volume</SelectItem>
                  <SelectItem value="git">Git Repository</SelectItem>
                  <SelectItem value="local">Local Directory</SelectItem>
                  <SelectItem value="object-store">Object Store Prefix</SelectItem>
                </SelectContent>
              </Select>
            </FormField>
            {formSourceKind === 'notebook-volume' ? (
              <FormField label="Notebook Volume" htmlFor="pipeline-volume">
                <Select value={formVolumeId} onValueChange={v => setFormVolumeId(v ?? '')}>
                  <SelectTrigger id="pipeline-volume"><SelectValue placeholder="— select a volume —" /></SelectTrigger>
                  <SelectContent>
                    {volumes.length === 0 ? (
                      <SelectItem value="__none__" disabled>No released volumes</SelectItem>
                    ) : volumes.map(v => (
                      <SelectItem key={v.id} value={v.id}>{v.label} · {v.work_dir}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </FormField>
            ) : formSourceKind === 'git' ? (
              <div className="space-y-3">
                <FormField
                  label={<>Git Credential <span className="font-normal text-muted-foreground/70">(optional)</span></>}
                  htmlFor="pipeline-git-credential"
                  helperText={
                    formCredential
                      ? 'Using the selected credential.'
                      : autoMatchedCredential
                        ? `Auto-matched: ${autoMatchedCredential.name} · ${autoMatchedCredential.endpoint}`
                        : 'Leave empty to auto-match a registered credential, or clone unauthenticated.'
                  }
                >
                  <Select value={formCredential || undefined} onValueChange={v => handleGitCredentialChange(v ?? '')}>
                    <SelectTrigger id="pipeline-git-credential"><SelectValue placeholder="Auto-match by repository URL" /></SelectTrigger>
                    <SelectContent>
                      {gitCredentials.length === 0 ? (
                        <SelectItem value="__none__" disabled>No active git credentials</SelectItem>
                      ) : gitCredentials.map(credential => (
                        <SelectItem key={credential.name} value={credential.name}>{credential.name}{credential.disabled ? ' (disabled)' : ''} · {credential.endpoint || 'any repo'}</SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </FormField>
                <FormField label="Repository URL" htmlFor="pipeline-git-repo">
                  <Input id="pipeline-git-repo" value={formRepo} onChange={e => setFormRepo(e.target.value)} placeholder="https://github.com/org/repo.git" />
                </FormField>
                <FormField label="Branch" htmlFor="pipeline-git-branch">
                  <Input id="pipeline-git-branch" value={formBranch} onChange={e => setFormBranch(e.target.value)} placeholder="main" />
                </FormField>
              </div>
            ) : (
              <FormField label="Source Root" htmlFor="pipeline-source-root">
                <Input id="pipeline-source-root" value={formRoot} onChange={e => setFormRoot(e.target.value)} placeholder="/workspaces/project" />
              </FormField>
            )}
            <FormActions
              submitLabel="Start Editing"
              submitDisabled={!canProceed}
              onCancel={() => navigate(`/projects/${projectId}/pipelines`)}
            />
          </form>
        </DataBodyTemplate.Group>
      </DataBodyTemplate>
    )
  }

  return (
    <>
      <WorkbenchBodyTemplate
        topBar={<PageTopBar left={`Pipeline Templates / ${editorFromVersion ? 'New Version' : 'Pipeline Editor'}`} />}
        title={editorFromVersion ? 'New Version' : 'Pipeline Editor'}
        description="Build a Piper Pipeline YAML from a source workspace, a task canvas, and a separate YAML tab."
        actions={
          <>
            <Button variant="outline" size="sm" onClick={validateNow}>Validate</Button>
            <Button size="sm" onClick={handleSubmit} disabled={submitting}>
              <Upload size={14} className="mr-1.5" /> Submit
            </Button>
          </>
        }
        leftPaneWidth={320}
        minLeftPaneWidth={240}
        maxLeftPaneWidth={440}
        rightPaneWidth={380}
        minRightPaneWidth={280}
        maxRightPaneWidth={520}
        leftPaneLabel="Task Palette"
        rightPaneLabel="Task Editor"
        leftPane={
          <div className="flex h-full flex-col">
            <div className="border-b border-border px-4 py-3">
              <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Pipeline Name</label>
              <Input value={pipelineName} onChange={e => setPipelineName(e.target.value)} />
            </div>
            <div className="space-y-4 overflow-y-auto p-4">
              <div>
               <h2 className="mb-3 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Runtime Defaults</h2>
               <div className="space-y-3">
                <div>
                  <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Runtime</label>
                  <Select
                    value={defaults.placementRuntime}
                    onValueChange={value => updateDefaults({ placementRuntime: value ?? '' })}
                    disabled={!!serverRuntime && (!defaults.placementRuntime || defaults.placementRuntime === serverRuntime)}
                  >
                    <SelectTrigger size="sm" className="w-full"><SelectValue placeholder="— select runtime —" /></SelectTrigger>
                    <SelectContent>
                      <SelectItem value="baremetal">Bare metal</SelectItem>
                      <SelectItem value="docker">Docker</SelectItem>
                      <SelectItem value="k8s">Kubernetes</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                {defaults.placementRuntime === 'k8s' && (
                  <>
                    <div>
                      <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Container Image</label>
                      <Input value={defaults.k8sImage} onChange={e => updateDefaults({ k8sImage: e.target.value })} />
                    </div>
                    <div>
                      <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Namespace</label>
                      <Input value={defaults.k8sNamespace} onChange={e => updateDefaults({ k8sNamespace: e.target.value })} />
                    </div>
                  </>
                )}
                {defaults.placementRuntime === 'docker' && (
                  <div>
                    <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Container Image</label>
                    <Input value={defaults.dockerImage} onChange={e => updateDefaults({ dockerImage: e.target.value })} />
                  </div>
                )}
               </div>
              </div>
              <div>
                <h2 className="mb-3 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Task Palette</h2>
                <div className="space-y-2">
                  {(['notebook', 'python', 'command'] as PipelineTaskType[]).map(type => (
                    <Button
                      key={type}
                      type="button"
                      variant="outline"
                      draggable
                      onDragStart={e => handlePaletteDragStart(e, type)}
                      onDragEnd={handlePaletteDragEnd}
                      onClick={() => addTask(type)}
                      className="h-auto w-full justify-between border-dashed py-3"
                    >
                      <div className="text-sm font-medium">{TASK_LABELS[type]}</div>
                      {TASK_ICONS[type]}
                    </Button>
                  ))}
                </div>
              </div>
            </div>
          </div>
        }
        mainPane={
          <div className="flex h-full flex-col overflow-hidden">
            <div className="flex shrink-0 items-center gap-3 border-b border-border px-4 py-2.5">
              <HardDrive size={14} className="shrink-0 text-muted-foreground" />
              <div className="min-w-0 flex-1">
                <span className="text-xs text-muted-foreground">Source Workspace · </span>
                {editorSourceKind === 'notebook-volume' && selectedVolume ? (
                  <>
                    <span className="text-sm font-medium">{selectedVolume.label}</span>
                    <span className="ml-2 font-mono text-xs text-muted-foreground">{selectedVolume.work_dir}</span>
                  </>
                ) : editorSourceKind === 'git' ? (
                  <>
                    <span className="text-sm font-medium">{editorCredential || 'auto-match'}</span>
                    <span className="ml-2 font-mono text-xs text-muted-foreground">{editorRepo}</span>
                    {editorBranch && <span className="ml-2 font-mono text-xs text-muted-foreground">{editorBranch}</span>}
                  </>
                ) : (
                  <span className="font-mono text-xs">{editorRoot || editorSourceKind}</span>
                )}
              </div>
              {canBrowse && volumeFilesStatus === 'transitioning' && (
                <span className="animate-pulse text-xs text-muted-foreground">Loading files…</span>
              )}
              {canBrowse && volumeFilesStatus === 'unavailable' && (
                <span className="text-xs text-destructive">Volume unavailable</span>
              )}
            </div>

            <Tabs value={activeTab} onValueChange={value => handleTabChange(value as ActiveTab)} className="flex min-h-0 flex-1 flex-col">
              <TabsList variant="line" className="shrink-0 border-b border-border px-4">
                <TabsTrigger value="design">Design</TabsTrigger>
                <TabsTrigger value="yaml">YAML</TabsTrigger>
              </TabsList>

              <TabsContent value="design" className="relative min-h-0 flex-1 overflow-hidden p-3">
                <div className="absolute right-5 top-5 z-10">
                  <Button variant="outline" size="sm" onClick={resetLayout}>Reset layout</Button>
                </div>
                <PipelineCanvas
                  steps={tasks}
                  positions={positions}
                  selectedId={selectedId}
                  resetKey={resetKey}
                  onSelectStep={setSelectedId}
                  onDoubleClickStep={openTaskEditor}
                  onAddStep={(type, position) => { dragDropHandledRef.current = true; addTask(type, position) }}
                  onMoveStep={(id, position) => setPositions(pos => ({ ...pos, [id]: position }))}
                  onConnectSteps={connectTasks}
                  onDisconnectSteps={disconnectTasks}
                />
              </TabsContent>

              <TabsContent value="yaml" className="min-h-0 flex-1 overflow-y-auto">
                <div className="p-4">
                  <div className="mb-3 flex items-center justify-between gap-3">
                    <div>
                      <h2 className="text-sm font-semibold">Pipeline YAML</h2>
                      <p className="text-xs text-muted-foreground">The YAML stays canonical. Apply it to rehydrate the task graph.</p>
                    </div>
                    <Button variant="outline" size="sm" onClick={applyYamlToDesign}>Apply YAML to Graph</Button>
                  </div>
                  <div className="space-y-3">
                    {yamlStatus.parseError ? (
                      <p className="text-sm text-destructive">{yamlStatus.parseError}</p>
                    ) : yamlStatus.difference ? (
                      <p className="text-xs text-amber-500">
                        YAML syntax is valid, but Design cannot preserve “{yamlStatus.difference}”. You can submit the original YAML from this tab.
                      </p>
                    ) : (
                      <p className="text-xs text-green-500">YAML is ready for Design. Server validation runs on submit.</p>
                    )}
                    {error && <p className="text-sm text-destructive">{error}</p>}
                    <YamlMirror
                      value={yamlText}
                      onChange={e => {
                        setYamlText(e.target.value)
                        pendingSubmitRef.current = null
                        setError('')
                      }}
                      className="min-h-136"
                    />
                  </div>
                </div>
              </TabsContent>
            </Tabs>
          </div>
        }
        rightPane={editingTask ? (
          <div className="flex h-full flex-col">
            <div className="flex shrink-0 items-center justify-between border-b border-border px-4 py-3">
              <div>
                <h2 className="text-sm font-semibold">Task Editor</h2>
                <p className="truncate text-xs text-muted-foreground">{editingTask.name}</p>
              </div>
              <div className="flex items-center gap-1">
                <IconButton icon={<ArrowUp />} label="Move up" onClick={() => moveTask(editingIndex, -1)} disabled={editingIndex === 0} />
                <IconButton icon={<ArrowDown />} label="Move down" onClick={() => moveTask(editingIndex, 1)} disabled={editingIndex === tasks.length - 1} />
                <IconButton icon={<Trash2 />} label="Delete task" onClick={() => removeTask(editingIndex)} className="text-destructive hover:bg-destructive/10" />
                <IconButton icon={<X />} label="Close" onClick={() => setEditingId(null)} />
              </div>
            </div>

            <div className="flex-1 space-y-4 overflow-y-auto p-4">
              <div className="grid gap-3 sm:grid-cols-2">
                <div>
                  <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Task Name</label>
                  <Input value={editingTask.name} onChange={e => updateTask(editingIndex, { name: e.target.value })} />
                </div>
                <div>
                  <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Task Type</label>
                  <Select value={editingTask.type} onValueChange={value => updateTask(editingIndex, { type: value as PipelineTaskType })}>
                    <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
                    <SelectContent>
                      <SelectItem value="command">Command</SelectItem>
                      <SelectItem value="python">Python</SelectItem>
                      <SelectItem value="notebook">Notebook</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
              </div>

              {(editingTask.type === 'notebook' || editingTask.type === 'python' || editingTask.type === 'command') && (
                <div>
                  <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">
                    {SOURCE_LABELS[editingTask.type]}
                  </label>
                  {editingTask.type === 'command' && (
                    <p className="mb-1 text-xs text-muted-foreground">
                      Optional. Path within the repo the command should treat as $PIPER_SCRIPT_PATH — a directory is fine. Leave empty to run from the repo root.
                    </p>
                  )}
                  <div ref={fileBrowserRef} className="relative">
                    <div className="flex gap-1.5">
                      <Input
                        value={editingTask.sourcePath}
                        onChange={e => updateTask(editingIndex, { sourcePath: e.target.value })}
                        placeholder={editingTask.type === 'notebook' ? 'workbook.ipynb' : editingTask.type === 'python' ? 'scripts/train.py' : 'scripts'}
                      />
                      {canBrowse && (
                        <IconButton
                          icon={<FolderOpen />}
                          label="Browse files in volume"
                          onClick={() => { setBrowseQuery(''); setFileBrowserOpen(o => !o) }}
                        />
                      )}
                    </div>
                    {fileBrowserOpen && canBrowse && (
                      <FileBrowseDropdown
                        ext={editingTask.type === 'notebook' ? '.ipynb' : '.py'}
                        files={volumeFiles.filter(f => f.endsWith(editingTask.type === 'notebook' ? '.ipynb' : '.py'))}
                        query={browseQuery}
                        onQueryChange={setBrowseQuery}
                        onSelect={f => { updateTask(editingIndex, { sourcePath: f }); setFileBrowserOpen(false) }}
                      />
                    )}
                  </div>
                </div>
              )}

              {editingTask.type !== 'notebook' && (
                <div>
                  <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Command</label>
                  <ShellMirror
                    value={editingTask.command.join('\n')}
                    onChange={e => updateTask(editingIndex, { command: e.target.value.split('\n') })}
                    minHeight="6rem"
                    placeholder={editingTask.type === 'python' ? 'python\nscript.py' : 'echo\nhello'}
                  />
                </div>
              )}

              {(editingTask.type === 'notebook' || editingTask.type === 'python') && (
                <div>
                  <div className="mb-2 flex items-center justify-between">
                    <label className="block text-[11px] uppercase tracking-wider text-muted-foreground">Source Dependencies</label>
                    <Button variant="outline" size="sm" onClick={() => addDep(editingIndex)}><Plus size={14} className="mr-1.5" /> Add</Button>
                  </div>
                  {editingTask.deps.length === 0 ? (
                    <p className="text-xs text-muted-foreground">No extra files or directories. Entry point only.</p>
                  ) : (
                    <div className="space-y-2">
                      {editingTask.deps.map((dep, di) => {
                        const browseKey = `dep-${di}`
                        const isBrowseOpen = artifactBrowseKey === browseKey
                        return (
                          <div key={di} ref={isBrowseOpen ? artifactBrowseRef : null} className="relative">
                            <div className="flex gap-2">
                              <Input
                                value={dep}
                                placeholder="models/  or  utils/helper.py"
                                onChange={e => updateDep(editingIndex, di, e.target.value)}
                              />
                              {canBrowse && (
                                <IconButton
                                  icon={<FolderOpen />}
                                  label="Browse volume files"
                                  onClick={() => { setBrowseQuery(''); setArtifactBrowseKey(k => k === browseKey ? null : browseKey) }}
                                />
                              )}
                              <IconButton icon={<Trash2 />} label="Remove" onClick={() => removeDep(editingIndex, di)} className="text-destructive hover:bg-destructive/10" />
                            </div>
                            {isBrowseOpen && (
                              <DepBrowseDropdown
                                files={volumeFiles}
                                query={browseQuery}
                                onQueryChange={setBrowseQuery}
                                onSelect={f => { updateDep(editingIndex, di, f); setArtifactBrowseKey(null) }}
                              />
                            )}
                          </div>
                        )
                      })}
                    </div>
                  )}
                  <p className="mt-1 text-[11px] text-muted-foreground">Append <code>/</code> for directories (e.g. <code>models/</code>). All contents are included in the snapshot.</p>
                </div>
              )}

              <PairSection
                label="Parameters"
                emptyText="No parameters."
                keyPlaceholder="name"
                items={editingTask.params}
                onAdd={() => addParamRow(editingIndex)}
                onRemove={i => removeParamRow(editingIndex, i)}
                onUpdate={(i, patch) => updateParamField(editingIndex, i, patch)}
              />

              <EnvVarEditor
                label="Environment"
                emptyText="No env overrides."
                items={editingTask.env}
                onAdd={() => addEnvRow(editingIndex)}
                onRemove={i => removeEnvRow(editingIndex, i)}
                onUpdate={(i, patch) => updateEnvField(editingIndex, i, patch)}
              />

              <ArtifactSection
                label="Inputs"
                kind="inputs"
                items={editingTask.inputs}
                canBrowse={canBrowse}
                volumeFiles={volumeFiles}
                activeBrowseKey={artifactBrowseKey}
                browseQuery={browseQuery}
                browseRef={artifactBrowseRef}
                onAdd={() => addArtifactRow(editingIndex, 'inputs')}
                onRemove={i => removeArtifactRow(editingIndex, 'inputs', i)}
                onUpdate={(i, patch) => updateArtifactField(editingIndex, 'inputs', i, patch)}
                onBrowseToggle={key => { setBrowseQuery(''); setArtifactBrowseKey(k => k === key ? null : key) }}
                onBrowseQueryChange={setBrowseQuery}
                onBrowseSelect={(i, f) => { updateArtifactField(editingIndex, 'inputs', i, { path: f }); setArtifactBrowseKey(null) }}
              />

              <ArtifactSection
                label="Outputs"
                kind="outputs"
                items={editingTask.outputs}
                canBrowse={canBrowse}
                volumeFiles={volumeFiles}
                activeBrowseKey={artifactBrowseKey}
                browseQuery={browseQuery}
                browseRef={artifactBrowseRef}
                onAdd={() => addArtifactRow(editingIndex, 'outputs')}
                onRemove={i => removeArtifactRow(editingIndex, 'outputs', i)}
                onUpdate={(i, patch) => updateArtifactField(editingIndex, 'outputs', i, patch)}
                onBrowseToggle={key => { setBrowseQuery(''); setArtifactBrowseKey(k => k === key ? null : key) }}
                onBrowseQueryChange={setBrowseQuery}
                onBrowseSelect={(i, f) => { updateArtifactField(editingIndex, 'outputs', i, { path: f }); setArtifactBrowseKey(null) }}
              />

              <div>
               <h2 className="mb-3 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Runtime Override</h2>
               <div className="space-y-3">
                <div>
                  <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Runtime</label>
                  <Select
                    value={editingTask.driver.placementRuntime || '__inherit__'}
                    onValueChange={value => updateTaskDriver(editingIndex, {
                      placementRuntime: value === '__inherit__' ? '' : (value ?? ''),
                    })}
                  >
                    <SelectTrigger size="sm" className="w-full"><SelectValue /></SelectTrigger>
                    <SelectContent>
                      <SelectItem value="__inherit__">Inherit</SelectItem>
                      <SelectItem value="baremetal">Bare metal</SelectItem>
                      <SelectItem value="docker">Docker</SelectItem>
                      <SelectItem value="k8s">Kubernetes</SelectItem>
                    </SelectContent>
                  </Select>
                  <p className="mt-1 text-xs text-muted-foreground">Leave as Inherit to use pipeline defaults.</p>
                </div>
                {editingTask.driver.placementRuntime === 'k8s' && (
                  <div className="grid gap-3 sm:grid-cols-2">
                    <div>
                      <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Container Image</label>
                      <Input
                        value={editingTask.driver.k8sImage}
                        onChange={e => updateTaskDriver(editingIndex, { k8sImage: e.target.value })}
                      />
                    </div>
                    <div>
                      <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Namespace</label>
                      <Input
                        value={editingTask.driver.k8sNamespace}
                        onChange={e => updateTaskDriver(editingIndex, { k8sNamespace: e.target.value })}
                      />
                    </div>
                  </div>
                )}
                {editingTask.driver.placementRuntime === 'docker' && (
                  <div>
                    <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Container Image</label>
                    <Input
                      value={editingTask.driver.dockerImage}
                      onChange={e => updateTaskDriver(editingIndex, { dockerImage: e.target.value })}
                    />
                  </div>
                )}
               </div>
              </div>

              <div className="grid grid-cols-3 gap-3">
                <div>
                  <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">CPU</label>
                  <Input value={editingTask.cpu} onChange={e => updateTask(editingIndex, { cpu: e.target.value })} placeholder="500m" />
                </div>
                <div>
                  <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Memory</label>
                  <Input value={editingTask.memory} onChange={e => updateTask(editingIndex, { memory: e.target.value })} placeholder="1Gi" />
                </div>
                <div>
                  <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">GPU</label>
                  <Input value={editingTask.gpu} onChange={e => updateTask(editingIndex, { gpu: e.target.value })} placeholder="1" />
                </div>
              </div>
            </div>
          </div>
        ) : undefined}
      />

      {submitModalOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
          <div className="w-full max-w-sm rounded-xl border border-border bg-card p-6 shadow-xl">
            <div className="mb-4 flex items-center justify-between">
              <h2 className="text-sm font-semibold">Submit Pipeline Template</h2>
              <Button type="button" variant="ghost" size="icon" onClick={closeSubmitModal} className="h-7 w-7">
                <X size={14} />
              </Button>
            </div>
            <p className="mb-4 text-xs text-muted-foreground">
              Submitting creates a new immutable snapshot of your source files in object storage.
              Each submission gets its own UUID — previous snapshots are untouched.
            </p>
            <div className="mb-4">
              <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">
                Notebook Volume <span className="normal-case text-muted-foreground/60">(optional — required for local source steps)</span>
              </label>
              <Select value={submitVolumeId} onValueChange={v => setSubmitVolumeId((v ?? '__none__') === '__none__' ? '' : (v ?? ''))}>
                <SelectTrigger><SelectValue placeholder="— none —" /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="__none__">— none —</SelectItem>
                  {volumes.map(v => (
                    <SelectItem key={v.id} value={v.id}>{v.label} · {v.work_dir}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            {error && <p className="mb-3 text-xs text-destructive">{error}</p>}
            <div className="flex justify-end gap-2">
              <Button variant="outline" size="sm" onClick={closeSubmitModal} disabled={submitting}>Edit YAML</Button>
              <Button size="sm" onClick={confirmSubmit} disabled={submitting}>
                <Upload size={14} className="mr-1.5" />
                {submitting ? 'Submitting…' : 'Confirm Submit'}
              </Button>
            </div>
          </div>
        </div>
      )}
    </>
  )
}
