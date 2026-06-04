import { useEffect, useMemo, useRef, useState, type DragEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  ArrowDown,
  ArrowUp,
  CalendarClock,
  ClipboardList,
  Code2,
  FileCode2,
  FolderOpen,
  Plus,
  Play,
  BookOpen,
  Trash2,
  X,
} from 'lucide-react'
import {
  DataPage,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { YamlMirror } from '@/components/ui/yaml-mirror'
import { IconButton } from '@/components/ui/icon-button'
import PipelineCanvas from '@/shared/components/PipelineCanvas'
import { createRun, listRuns, type Run } from '@/features/runs/api'
import { listNotebookVolumes, listVolumeFiles, type NotebookVolume } from '@/features/notebooks/api'
import {
  buildPipelineDraftYaml,
  defaultPipelineDraft,
  defaultPipelineStep,
  parsePipelineDraftYaml,
  validatePipelineDraft,
  type PipelineArtifactDraft,
  type PipelineKeyValueDraft,
  type PipelineStepDraft,
  type PipelineTaskType,
} from '@/features/pipelines/editor'

const SESSION_DRAFT_KEY = 'piper.pipeline.editor.draft'

type SourceKind = 'notebook-volume' | 'git' | 'local' | 'object-store'
type ActiveTab = 'design' | 'yaml'

function buildPositions(tasks: PipelineStepDraft[]): Record<string, { x: number; y: number }> {
  const depth = new Map<string, number>()
  const byName = new Map(tasks.map(task => [task.name, task]))

  const walk = (name: string, seen = new Set<string>()): number => {
    if (depth.has(name)) return depth.get(name) ?? 0
    if (seen.has(name)) return 0
    seen.add(name)
    const task = byName.get(name)
    if (!task || task.dependsOn.length === 0) {
      depth.set(name, 0)
      return 0
    }
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

  const positions: Record<string, { x: number; y: number }> = {}
  const NODE_W = 248
  const NODE_H = 96
  const GAP_X = 96
  const GAP_Y = 28

  for (const [col, names] of columns) {
    names.forEach((name, row) => {
      const task = byName.get(name)
      if (!task) return
      positions[task.id] = {
        x: col * (NODE_W + GAP_X) + 24,
        y: row * (NODE_H + GAP_Y) + 24,
      }
    })
  }

  if (Object.keys(positions).length === 0) {
    tasks.forEach((task, index) => {
      positions[task.id] = {
        x: 24 + (index % 3) * 300,
        y: 24 + Math.floor(index / 3) * 140,
      }
    })
  }

  return positions
}

function emptyPair(): PipelineKeyValueDraft {
  return { key: '', value: '' }
}

function emptyArtifact(): PipelineArtifactDraft {
  return { name: '', path: '', from: '' }
}

function taskLabel(type: PipelineTaskType): string {
  if (type === 'notebook') return 'Notebook Task'
  if (type === 'python') return 'Python Task'
  return 'Command Task'
}

function sourceLabel(type: PipelineTaskType): string {
  if (type === 'notebook') return 'Notebook file'
  if (type === 'python') return 'Script file'
  return 'Working path'
}

function defaultTask(type: PipelineTaskType, index = 0): PipelineStepDraft {
  const draft = defaultPipelineStep(index, type)
  draft.dependsOn = []
  if (type === 'python') {
    draft.command = ['python', 'task.py']
  } else if (type === 'notebook') {
    draft.command = []
  }
  return draft
}

export default function PipelineEditorPage() {
  const navigate = useNavigate()
  const initialDraft = useMemo(() => defaultPipelineDraft(), [])
  const [activeTab, setActiveTab] = useState<ActiveTab>('design')
  const [pipelineName, setPipelineName] = useState(initialDraft.name)
  const [sourceKind, setSourceKind] = useState<SourceKind>('notebook-volume')
  const [sourceRoot, setSourceRoot] = useState('')
  const [sourceVolumeId, setSourceVolumeId] = useState('')
  const [volumes, setVolumes] = useState<NotebookVolume[]>([])
  const [volumeFiles, setVolumeFiles] = useState<string[]>([])
  const [tasks, setTasks] = useState<PipelineStepDraft[]>(initialDraft.steps)
  const [positions, setPositions] = useState<Record<string, { x: number; y: number }>>(() => buildPositions(initialDraft.steps))
  const [selectedId, setSelectedId] = useState<string>(initialDraft.steps[0]?.id ?? '')
  const [editingId, setEditingId] = useState<string | null>(null)
  const [fileBrowserOpen, setFileBrowserOpen] = useState(false)
  const fileBrowserRef = useRef<HTMLDivElement>(null)
  const [artifactBrowseKey, setArtifactBrowseKey] = useState<string | null>(null)
  const artifactBrowseRef = useRef<HTMLDivElement>(null)
  const [yamlText, setYamlText] = useState(() => buildPipelineDraftYaml({ name: initialDraft.name, steps: initialDraft.steps }))
  const [validation, setValidation] = useState<string[]>([])
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [runs, setRuns] = useState<Run[]>([])
  const [resetKey, setResetKey] = useState(0)
  const draggingTaskTypeRef = useRef<PipelineTaskType | null>(null)
  const dragDropHandledRef = useRef(false)

  useEffect(() => {
    listRuns().then(setRuns).catch(() => {})
  }, [])

  useEffect(() => {
    listNotebookVolumes()
      .then(setVolumes)
      .catch(() => setVolumes([]))
  }, [])

  useEffect(() => {
    if (!fileBrowserOpen && !artifactBrowseKey) return
    const handler = (e: MouseEvent) => {
      if (fileBrowserRef.current && !fileBrowserRef.current.contains(e.target as Node)) {
        setFileBrowserOpen(false)
      }
      if (artifactBrowseRef.current && !artifactBrowseRef.current.contains(e.target as Node)) {
        setArtifactBrowseKey(null)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [fileBrowserOpen, artifactBrowseKey])

  useEffect(() => {
    if (sourceKind !== 'notebook-volume' || !sourceVolumeId) {
      setVolumeFiles([])
      return
    }
    listVolumeFiles(sourceVolumeId)
      .then(setVolumeFiles)
      .catch(() => setVolumeFiles([]))
  }, [sourceKind, sourceVolumeId])

  useEffect(() => {
    if (sourceKind !== 'notebook-volume') return
    const selected = volumes.find(volume => volume.id === sourceVolumeId)
    if (selected && selected.work_dir && sourceRoot !== selected.work_dir) {
      setSourceRoot(selected.work_dir)
    }
  }, [sourceKind, sourceVolumeId, sourceRoot, volumes])

  useEffect(() => {
    const nextYaml = buildPipelineDraftYaml({ name: pipelineName, steps: tasks })
    setYamlText(nextYaml)
    setValidation(validatePipelineDraft({ name: pipelineName, steps: tasks }))
    if (!tasks.some(task => task.id === selectedId)) {
      setSelectedId(tasks[0]?.id ?? '')
    }
    if (editingId && !tasks.some(task => task.id === editingId)) {
      setEditingId(null)
    }
  }, [pipelineName, tasks, selectedId, editingId])

  useEffect(() => {
    setPositions(prev => {
      const next = { ...prev }
      const known = new Set(tasks.map(task => task.id))
      for (const task of tasks) {
        if (!next[task.id]) {
          next[task.id] = buildPositions(tasks)[task.id] ?? { x: 24, y: 24 }
        }
      }
      for (const id of Object.keys(next)) {
        if (!known.has(id)) delete next[id]
      }
      return next
    })
  }, [tasks])

  const editingIndex = useMemo(() => tasks.findIndex(task => task.id === editingId), [tasks, editingId])
  const editingTask = editingIndex >= 0 ? tasks[editingIndex] : null
  const recentRuns = useMemo(() => {
    const name = pipelineName.trim()
    return name ? runs.filter(run => run.pipeline_name === name).slice(0, 10) : runs.slice(0, 10)
  }, [pipelineName, runs])
  const selectedVolume = useMemo(
    () => volumes.find(volume => volume.id === sourceVolumeId) ?? null,
    [sourceVolumeId, volumes],
  )

  function updateTask(index: number, patch: Partial<PipelineStepDraft>) {
    setTasks(current => {
      const next = current.map((task, i) => (i !== index ? task : { ...task, ...patch }))
      const prev = current[index]
      const nextName = patch.name?.trim()
      if (prev && nextName && nextName !== prev.name) {
        next.forEach((task, i) => {
          if (i === index) return
          task.dependsOn = task.dependsOn.map(dep => (dep === prev.name ? nextName : dep))
        })
      }
      return next
    })
  }

  function addTask(type: PipelineTaskType = 'command', position?: { x: number; y: number }) {
    setTasks(current => {
      const next = [...current, defaultTask(type, current.length)]
      const created = next[next.length - 1]
      setSelectedId(created.id)
      if (position) {
        setPositions(currentPositions => ({ ...currentPositions, [created.id]: position }))
      }
      return next
    })
  }

  function removeTask(index: number) {
    setTasks(current => {
      const removed = current[index]
      const next = current
        .filter((_, i) => i !== index)
        .map(task => ({
          ...task,
          dependsOn: task.dependsOn.filter(dep => dep !== removed?.name),
        }))
      if (removed) {
        setPositions(currentPositions => {
          const nextPositions = { ...currentPositions }
          delete nextPositions[removed.id]
          return nextPositions
        })
      }
      if (selectedId === removed?.id) {
        setSelectedId(next[0]?.id ?? '')
      }
      if (editingId === removed?.id) {
        setEditingId(null)
      }
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
      const source = current.find(task => task.id === sourceId)
      const targetIndex = current.findIndex(task => task.id === targetId)
      if (!source || targetIndex < 0 || source.id === targetId) return current
      const target = current[targetIndex]
      const next = current.map(task => {
        if (task.id !== target.id) return task
        return { ...task, dependsOn: Array.from(new Set([...task.dependsOn, source.name])) }
      })
      setSelectedId(target.id)
      return next
    })
  }

  function resetLayout() {
    setPositions(buildPositions(tasks))
    setResetKey(k => k + 1)
  }

  function disconnectTasks(sourceId: string, targetId: string) {
    setTasks(current => {
      const source = current.find(t => t.id === sourceId)
      if (!source) return current
      return current.map(task => {
        if (task.id !== targetId) return task
        return { ...task, dependsOn: task.dependsOn.filter(dep => dep !== source.name) }
      })
    })
  }

  function openTaskEditor(id: string) {
    setSelectedId(id)
    setEditingId(id)
  }

  function applyYaml() {
    try {
      const parsed = parsePipelineDraftYaml(yamlText)
      setPipelineName(parsed.name)
      setTasks(parsed.steps)
      setPositions(buildPositions(parsed.steps))
      setSelectedId(parsed.steps[0]?.id ?? '')
      setEditingId(null)
      setError('')
      setActiveTab('design')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  async function handleRun() {
    setSubmitting(true)
    setError('')
    try {
      const yaml = buildPipelineDraftYaml({ name: pipelineName, steps: tasks })
      const { run_id } = await createRun(yaml)
      navigate(`/runs/${run_id}`)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSubmitting(false)
    }
  }

  function handleSchedule() {
    sessionStorage.setItem(SESSION_DRAFT_KEY, buildPipelineDraftYaml({ name: pipelineName, steps: tasks }))
    navigate('/schedules/create')
  }

  function validateNow() {
    const messages = validatePipelineDraft({ name: pipelineName, steps: tasks })
    setValidation(messages)
    setError(messages[0] || '')
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
    if (!dragDropHandledRef.current && type) {
      addTask(type)
    }
    dragDropHandledRef.current = false
  }

  function updateTaskField(index: number, field: keyof PipelineStepDraft, value: string) {
    updateTask(index, { [field]: value } as Partial<PipelineStepDraft>)
  }

  function updateArtifactField(
    index: number,
    kind: 'inputs' | 'outputs',
    rowIndex: number,
    patch: Partial<PipelineArtifactDraft>,
  ) {
    setTasks(current => current.map((task, i) => {
      if (i !== index) return task
      const items = [...task[kind]]
      items[rowIndex] = { ...items[rowIndex], ...patch }
      return { ...task, [kind]: items } as PipelineStepDraft
    }))
  }

  function addArtifactRow(index: number, kind: 'inputs' | 'outputs') {
    setTasks(current => current.map((task, i) => {
      if (i !== index) return task
      return { ...task, [kind]: [...task[kind], emptyArtifact()] } as PipelineStepDraft
    }))
  }

  function removeArtifactRow(index: number, kind: 'inputs' | 'outputs', rowIndex: number) {
    setTasks(current => current.map((task, i) => {
      if (i !== index) return task
      return { ...task, [kind]: task[kind].filter((_, itemIndex) => itemIndex !== rowIndex) } as PipelineStepDraft
    }))
  }

  function updatePairField(
    index: number,
    kind: 'params' | 'env',
    rowIndex: number,
    patch: Partial<PipelineKeyValueDraft>,
  ) {
    setTasks(current => current.map((task, i) => {
      if (i !== index) return task
      const items = [...task[kind]]
      items[rowIndex] = { ...items[rowIndex], ...patch }
      return { ...task, [kind]: items } as PipelineStepDraft
    }))
  }

  function addPairRow(index: number, kind: 'params' | 'env') {
    setTasks(current => current.map((task, i) => {
      if (i !== index) return task
      return { ...task, [kind]: [...task[kind], emptyPair()] } as PipelineStepDraft
    }))
  }

  function removePairRow(index: number, kind: 'params' | 'env', rowIndex: number) {
    setTasks(current => current.map((task, i) => {
      if (i !== index) return task
      return { ...task, [kind]: task[kind].filter((_, itemIndex) => itemIndex !== rowIndex) } as PipelineStepDraft
    }))
  }

  const sourceSummary = sourceKind === 'notebook-volume'
    ? (selectedVolume ? `${selectedVolume.label} · ${selectedVolume.work_dir}` : 'Choose a notebook volume to seed the workspace root.')
    : sourceRoot || 'Set a source root for the pipeline workspace.'

  return (
    <DataPage>
      <DataPage.Header>
        <DataPage.TitleBlock
          title="Pipeline Editor"
          description="Build a Piper Pipeline YAML from a source workspace, a task canvas, and a separate YAML tab."
        />
        <DataPage.Actions>
          <Button variant="outline" size="sm" onClick={validateNow}>Validate</Button>
          <Button size="sm" onClick={handleRun} disabled={submitting}>
            <Play size={14} className="mr-1.5" /> Run
          </Button>
          <Button variant="outline" size="sm" onClick={handleSchedule}>
            <CalendarClock size={14} className="mr-1.5" /> Schedule
          </Button>
          <Button variant="ghost" size="sm" onClick={() => navigate('/history')}>
            <ClipboardList size={14} className="mr-1.5" /> Inspect
          </Button>
        </DataPage.Actions>
      </DataPage.Header>

      <DataPage.Content>
        <Tabs value={activeTab} onValueChange={(value) => setActiveTab(value as ActiveTab)}>
          <TabsList variant="line" className="mb-4">
            <TabsTrigger value="design">Design</TabsTrigger>
            <TabsTrigger value="yaml">YAML</TabsTrigger>
          </TabsList>

          <TabsContent value="design" className="min-h-0">
            <div className={`grid gap-4 ${editingTask ? 'xl:grid-cols-[320px_minmax(0,1fr)_380px]' : 'xl:grid-cols-[320px_minmax(0,1fr)]'} transition-none`}>
              <DataPage.Group surface="bordered" className="min-h-0">
                <div className="border-b border-border px-4 py-3">
                  <label className="mb-1 block text-xs text-muted-foreground">Pipeline Name</label>
                  <Input value={pipelineName} onChange={e => setPipelineName(e.target.value)} />
                </div>

                <div className="space-y-4 p-4">
                  <div>
                    <h2 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Source Workspace</h2>
                    <div className="mt-2 space-y-3">
                      <div>
                        <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Source Type</label>
                        <Select value={sourceKind} onValueChange={(value) => setSourceKind(value as SourceKind)}>
                          <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
                          <SelectContent>
                            <SelectItem value="notebook-volume">Notebook Volume</SelectItem>
                            <SelectItem value="git">Git Repository</SelectItem>
                            <SelectItem value="local">Local Directory</SelectItem>
                            <SelectItem value="object-store">Object Store Prefix</SelectItem>
                          </SelectContent>
                        </Select>
                      </div>

                      {sourceKind === 'notebook-volume' ? (
                        <div>
                          <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Notebook Volume</label>
                          <Select value={sourceVolumeId} onValueChange={value => setSourceVolumeId(value ?? '')}>
                            <SelectTrigger size="sm"><SelectValue placeholder="— select volume —" /></SelectTrigger>
                            <SelectContent>
                              {volumes.length === 0 ? (
                                <SelectItem value="__none__" disabled>No released volumes</SelectItem>
                              ) : volumes.map(volume => (
                                <SelectItem key={volume.id} value={volume.id}>
                                  {volume.label} · {volume.work_dir}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        </div>
                      ) : (
                        <div>
                          <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Source Root</label>
                          <Input value={sourceRoot} onChange={e => setSourceRoot(e.target.value)} placeholder="/workspaces/project" />
                        </div>
                      )}

                      <div className="rounded-lg border border-border bg-card px-3 py-2 text-xs text-muted-foreground">
                        {sourceSummary}
                      </div>
                    </div>
                  </div>

                  <div>
                    <div className="flex items-center justify-between">
                      <h2 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Task Palette</h2>
                    </div>
                    <div className="mt-3 space-y-2">
                      {(['notebook', 'python', 'command'] as PipelineTaskType[]).map(type => (
                        <button
                          key={type}
                          type="button"
                          draggable
                          onDragStart={(e) => handlePaletteDragStart(e, type)}
                          onDragEnd={handlePaletteDragEnd}
                          onClick={() => addTask(type)}
                          className="flex w-full items-center justify-between rounded-xl border border-dashed border-border bg-background px-3 py-3 text-left transition hover:border-primary hover:bg-accent"
                        >
                          <div>
                            <div className="text-sm font-medium">{taskLabel(type)}</div>
                            <div className="text-xs text-muted-foreground">
                              Drag onto the canvas or click to add.
                            </div>
                          </div>
                          {type === 'notebook' ? (
                            <BookOpen size={16} className="text-muted-foreground" />
                          ) : type === 'python' ? (
                            <Code2 size={16} className="text-muted-foreground" />
                          ) : (
                            <FileCode2 size={16} className="text-muted-foreground" />
                          )}
                        </button>
                      ))}
                    </div>
                  </div>

                  <div className="rounded-xl border border-border bg-card px-3 py-3 text-xs text-muted-foreground">
                    <div className="font-medium text-foreground">Canvas-first editing</div>
                    <p className="mt-1">
                      Drag a task into the canvas or click one of the palette tiles. Double-click the node to edit its source file, parameters, and artifacts.
                    </p>
                    <div className="mt-2 text-[11px] uppercase tracking-wider text-muted-foreground">
                      {tasks.length} tasks on canvas
                    </div>
                  </div>
                </div>
              </DataPage.Group>

              <DataPage.Group surface="bordered" className="min-h-0">
                <div className="border-b border-border px-4 py-3">
                  <div className="flex items-center justify-between gap-3">
                    <div>
                      <h2 className="text-sm font-semibold">Task Canvas</h2>
                      <p className="text-xs text-muted-foreground">Drag tasks, connect them, and double-click any node to edit it.</p>
                    </div>
                    <Button variant="outline" size="sm" onClick={resetLayout}>Reset layout</Button>
                  </div>
                </div>
                <div className="p-3">
                  <PipelineCanvas
                    steps={tasks}
                    positions={positions}
                    selectedId={selectedId}
                    resetKey={resetKey}
                    onSelectStep={setSelectedId}
                    onDoubleClickStep={openTaskEditor}
                    onAddStep={(type, position) => {
                      dragDropHandledRef.current = true
                      addTask(type, position)
                    }}
                    onMoveStep={(id, position) => setPositions(current => ({ ...current, [id]: position }))}
                    onConnectSteps={connectTasks}
                    onDisconnectSteps={disconnectTasks}
                  />
                </div>
              </DataPage.Group>

              {editingTask && (
                <DataPage.Group surface="bordered" className="min-h-0 flex flex-col">
                  <div className="flex items-center justify-between border-b border-border px-4 py-3">
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

                    {(editingTask.type === 'notebook' || editingTask.type === 'python') && (
                      <div>
                        <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">{sourceLabel(editingTask.type)}</label>
                        {(() => {
                          const ext = editingTask.type === 'notebook' ? '.ipynb' : '.py'
                          const suggestions = volumeFiles.filter(f => f.endsWith(ext))
                          const canBrowse = sourceKind === 'notebook-volume'
                          return (
                            <div ref={fileBrowserRef} className="relative">
                              <div className="flex gap-1.5">
                                <Input
                                  value={editingTask.sourcePath}
                                  onChange={e => updateTask(editingIndex, { sourcePath: e.target.value })}
                                  placeholder={editingTask.type === 'notebook' ? 'workbook.ipynb' : 'scripts/train.py'}
                                />
                                {canBrowse && (
                                  <button
                                    type="button"
                                    title="Browse files in volume"
                                    onClick={() => setFileBrowserOpen(o => !o)}
                                    className="flex shrink-0 items-center justify-center rounded-lg border border-border bg-background px-2 text-muted-foreground transition hover:bg-accent hover:text-foreground"
                                  >
                                    <FolderOpen size={14} />
                                  </button>
                                )}
                              </div>
                              {fileBrowserOpen && canBrowse && (
                                <div className="absolute left-0 right-0 top-full z-50 mt-1 overflow-hidden rounded-lg border border-border bg-card shadow-lg">
                                  {!sourceVolumeId ? (
                                    <p className="px-3 py-3 text-xs text-muted-foreground">
                                      Select a notebook volume in Source Workspace to browse files.
                                    </p>
                                  ) : suggestions.length === 0 ? (
                                    <p className="px-3 py-3 text-xs text-muted-foreground">
                                      No {ext} files found in this volume.
                                    </p>
                                  ) : (
                                    <div className="max-h-52 overflow-y-auto">
                                      {suggestions.map(f => (
                                        <button
                                          key={f}
                                          type="button"
                                          className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-accent"
                                          onClick={() => {
                                            updateTask(editingIndex, { sourcePath: f })
                                            setFileBrowserOpen(false)
                                          }}
                                        >
                                          <span className="font-mono text-muted-foreground">{ext}</span>
                                          <span className="truncate">{f}</span>
                                        </button>
                                      ))}
                                    </div>
                                  )}
                                </div>
                              )}
                            </div>
                          )
                        })()}
                      </div>
                    )}

                    {editingTask.type !== 'notebook' && (
                      <div>
                        <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Command</label>
                        <textarea
                          className="min-h-[6rem] w-full rounded-md border border-border bg-background px-3 py-2 text-sm font-mono outline-none"
                          value={editingTask.command.join('\n')}
                          onChange={e => updateTask(editingIndex, { command: e.target.value.split(/\n+/).map(s => s.trim()).filter(Boolean) })}
                          placeholder={editingTask.type === 'python' ? 'python\nscript.py' : 'echo\nhello'}
                        />
                      </div>
                    )}

                    <div>
                      <div className="mb-2 flex items-center justify-between">
                        <label className="block text-[11px] uppercase tracking-wider text-muted-foreground">Parameters</label>
                        <Button variant="outline" size="sm" onClick={() => addPairRow(editingIndex, 'params')}><Plus size={14} className="mr-1.5" /> Add</Button>
                      </div>
                      <div className="space-y-2">
                        {editingTask.params.length === 0 ? (
                          <p className="text-xs text-muted-foreground">No parameters.</p>
                        ) : editingTask.params.map((param, rowIndex) => (
                          <div key={`${param.key}-${rowIndex}`} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] gap-2">
                            <Input value={param.key} placeholder="name" onChange={e => updatePairField(editingIndex, 'params', rowIndex, { key: e.target.value })} />
                            <Input value={param.value} placeholder="value" onChange={e => updatePairField(editingIndex, 'params', rowIndex, { value: e.target.value })} />
                            <IconButton icon={<Trash2 />} label="Remove" onClick={() => removePairRow(editingIndex, 'params', rowIndex)} className="text-destructive hover:bg-destructive/10" />
                          </div>
                        ))}
                      </div>
                    </div>

                    <div>
                      <div className="mb-2 flex items-center justify-between">
                        <label className="block text-[11px] uppercase tracking-wider text-muted-foreground">Environment</label>
                        <Button variant="outline" size="sm" onClick={() => addPairRow(editingIndex, 'env')}><Plus size={14} className="mr-1.5" /> Add</Button>
                      </div>
                      <div className="space-y-2">
                        {editingTask.env.length === 0 ? (
                          <p className="text-xs text-muted-foreground">No env overrides.</p>
                        ) : editingTask.env.map((entry, rowIndex) => (
                          <div key={`${entry.key}-${rowIndex}`} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] gap-2">
                            <Input value={entry.key} placeholder="NAME" onChange={e => updatePairField(editingIndex, 'env', rowIndex, { key: e.target.value })} />
                            <Input value={entry.value} placeholder="value" onChange={e => updatePairField(editingIndex, 'env', rowIndex, { value: e.target.value })} />
                            <IconButton icon={<Trash2 />} label="Remove" onClick={() => removePairRow(editingIndex, 'env', rowIndex)} className="text-destructive hover:bg-destructive/10" />
                          </div>
                        ))}
                      </div>
                    </div>

                    <div>
                      <div className="mb-2 flex items-center justify-between">
                        <label className="block text-[11px] uppercase tracking-wider text-muted-foreground">Inputs</label>
                        <Button variant="outline" size="sm" onClick={() => addArtifactRow(editingIndex, 'inputs')}><Plus size={14} className="mr-1.5" /> Add</Button>
                      </div>
                      <div className="space-y-2">
                        {editingTask.inputs.length === 0 ? (
                          <p className="text-xs text-muted-foreground">No inputs.</p>
                        ) : editingTask.inputs.map((input, rowIndex) => {
                          const browseKey = `inputs-${rowIndex}`
                          return (
                            <div key={`${input.name}-${rowIndex}`} className="grid gap-1">
                              <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2">
                                <Input value={input.name} placeholder="name" onChange={e => updateArtifactField(editingIndex, 'inputs', rowIndex, { name: e.target.value })} />
                                <IconButton icon={<Trash2 />} label="Remove" onClick={() => removeArtifactRow(editingIndex, 'inputs', rowIndex)} className="text-destructive hover:bg-destructive/10" />
                              </div>
                              <div ref={artifactBrowseKey === browseKey ? artifactBrowseRef : null} className="relative">
                                <div className="flex gap-1.5">
                                  <Input value={input.path} placeholder="path in workspace" onChange={e => updateArtifactField(editingIndex, 'inputs', rowIndex, { path: e.target.value })} />
                                  {sourceKind === 'notebook-volume' && (
                                    <button
                                      type="button"
                                      title="Browse volume files"
                                      onClick={() => setArtifactBrowseKey(k => k === browseKey ? null : browseKey)}
                                      className="flex shrink-0 items-center justify-center rounded-lg border border-border bg-background px-2 text-muted-foreground transition hover:bg-accent hover:text-foreground"
                                    >
                                      <FolderOpen size={14} />
                                    </button>
                                  )}
                                </div>
                                {artifactBrowseKey === browseKey && (
                                  <div className="absolute left-0 right-0 top-full z-50 mt-1 overflow-hidden rounded-lg border border-border bg-card shadow-lg">
                                    {!sourceVolumeId ? (
                                      <p className="px-3 py-3 text-xs text-muted-foreground">Select a notebook volume in Source Workspace to browse files.</p>
                                    ) : volumeFiles.length === 0 ? (
                                      <p className="px-3 py-3 text-xs text-muted-foreground">No files found in this volume.</p>
                                    ) : (
                                      <div className="max-h-52 overflow-y-auto">
                                        {volumeFiles.map(f => (
                                          <button
                                            key={f}
                                            type="button"
                                            className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-accent"
                                            onClick={() => {
                                              updateArtifactField(editingIndex, 'inputs', rowIndex, { path: f })
                                              setArtifactBrowseKey(null)
                                            }}
                                          >
                                            <span className="truncate font-mono">{f}</span>
                                          </button>
                                        ))}
                                      </div>
                                    )}
                                  </div>
                                )}
                              </div>
                              <Input value={input.from} placeholder="from (task name)" onChange={e => updateArtifactField(editingIndex, 'inputs', rowIndex, { from: e.target.value })} />
                            </div>
                          )
                        })}
                      </div>
                    </div>

                    <div>
                      <div className="mb-2 flex items-center justify-between">
                        <label className="block text-[11px] uppercase tracking-wider text-muted-foreground">Outputs</label>
                        <Button variant="outline" size="sm" onClick={() => addArtifactRow(editingIndex, 'outputs')}><Plus size={14} className="mr-1.5" /> Add</Button>
                      </div>
                      <div className="space-y-2">
                        {editingTask.outputs.length === 0 ? (
                          <p className="text-xs text-muted-foreground">No outputs.</p>
                        ) : editingTask.outputs.map((output, rowIndex) => {
                          const browseKey = `outputs-${rowIndex}`
                          return (
                            <div key={`${output.name}-${rowIndex}`} className="grid gap-1">
                              <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2">
                                <Input value={output.name} placeholder="name" onChange={e => updateArtifactField(editingIndex, 'outputs', rowIndex, { name: e.target.value })} />
                                <IconButton icon={<Trash2 />} label="Remove" onClick={() => removeArtifactRow(editingIndex, 'outputs', rowIndex)} className="text-destructive hover:bg-destructive/10" />
                              </div>
                              <div ref={artifactBrowseKey === browseKey ? artifactBrowseRef : null} className="relative">
                                <div className="flex gap-1.5">
                                  <Input value={output.path} placeholder="path in workspace" onChange={e => updateArtifactField(editingIndex, 'outputs', rowIndex, { path: e.target.value })} />
                                  {sourceKind === 'notebook-volume' && (
                                    <button
                                      type="button"
                                      title="Browse volume files"
                                      onClick={() => setArtifactBrowseKey(k => k === browseKey ? null : browseKey)}
                                      className="flex shrink-0 items-center justify-center rounded-lg border border-border bg-background px-2 text-muted-foreground transition hover:bg-accent hover:text-foreground"
                                    >
                                      <FolderOpen size={14} />
                                    </button>
                                  )}
                                </div>
                                {artifactBrowseKey === browseKey && (
                                  <div className="absolute left-0 right-0 top-full z-50 mt-1 overflow-hidden rounded-lg border border-border bg-card shadow-lg">
                                    {!sourceVolumeId ? (
                                      <p className="px-3 py-3 text-xs text-muted-foreground">Select a notebook volume in Source Workspace to browse files.</p>
                                    ) : volumeFiles.length === 0 ? (
                                      <p className="px-3 py-3 text-xs text-muted-foreground">No files found in this volume.</p>
                                    ) : (
                                      <div className="max-h-52 overflow-y-auto">
                                        {volumeFiles.map(f => (
                                          <button
                                            key={f}
                                            type="button"
                                            className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-accent"
                                            onClick={() => {
                                              updateArtifactField(editingIndex, 'outputs', rowIndex, { path: f })
                                              setArtifactBrowseKey(null)
                                            }}
                                          >
                                            <span className="truncate font-mono">{f}</span>
                                          </button>
                                        ))}
                                      </div>
                                    )}
                                  </div>
                                )}
                              </div>
                              <Input value={output.from} placeholder="from (task name)" onChange={e => updateArtifactField(editingIndex, 'outputs', rowIndex, { from: e.target.value })} />
                            </div>
                          )
                        })}
                      </div>
                    </div>

                    <div className="grid gap-3 grid-cols-3">
                      <div>
                        <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">CPU</label>
                        <Input value={editingTask.cpu} onChange={e => updateTaskField(editingIndex, 'cpu', e.target.value)} placeholder="500m" />
                      </div>
                      <div>
                        <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Memory</label>
                        <Input value={editingTask.memory} onChange={e => updateTaskField(editingIndex, 'memory', e.target.value)} placeholder="1Gi" />
                      </div>
                      <div>
                        <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">GPU</label>
                        <Input value={editingTask.gpu} onChange={e => updateTaskField(editingIndex, 'gpu', e.target.value)} placeholder="1" />
                      </div>
                    </div>

                    <div>
                      <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Depends On</label>
                      <Input
                        value={editingTask.dependsOn.join(', ')}
                        onChange={e => updateTask(editingIndex, { dependsOn: e.target.value.split(/[\n,]/).map(s => s.trim()).filter(Boolean) })}
                        placeholder="task-1, task-2"
                      />
                    </div>
                  </div>
                </DataPage.Group>
              )}
            </div>
          </TabsContent>

          <TabsContent value="yaml" className="min-h-0">
            <DataPage.Group surface="bordered">
              <div className="border-b border-border px-4 py-3">
                <div className="flex items-center justify-between gap-3">
                  <div>
                    <h2 className="text-sm font-semibold">Pipeline YAML</h2>
                    <p className="text-xs text-muted-foreground">The YAML stays canonical. Apply it to rehydrate the task graph.</p>
                  </div>
                  <Button variant="outline" size="sm" onClick={applyYaml}>Apply YAML to Graph</Button>
                </div>
              </div>
              <div className="space-y-3 p-4">
                {validation.length === 0 ? (
                  <p className="text-xs text-green-300">Draft looks valid.</p>
                ) : (
                  <ul className="space-y-1 text-sm text-red-300">
                    {validation.map(msg => <li key={msg}>• {msg}</li>)}
                  </ul>
                )}
                {error && <p className="text-sm text-destructive">{error}</p>}
                <YamlMirror
                  value={yamlText}
                  onChange={(e) => setYamlText(e.target.value)}
                  className="min-h-[34rem]"
                />
              </div>
            </DataPage.Group>
          </TabsContent>
        </Tabs>

        <DataPage.Group surface="bordered" className="mt-4">
          <DataPage.GroupHeader title="Recent Runs" className="px-4 pt-3" />
          <div className="overflow-hidden px-4 pb-4">
            {recentRuns.length === 0 ? (
              <p className="py-4 text-sm text-muted-foreground">No runs yet for this pipeline.</p>
            ) : (
              <div className="space-y-2">
                {recentRuns.map(run => (
                  <button
                    key={run.id}
                    type="button"
                    className="flex w-full items-center justify-between rounded-lg border border-border bg-card px-3 py-2 text-left hover:bg-accent"
                    onClick={() => navigate(`/runs/${run.id}`)}
                  >
                    <div>
                      <div className="font-mono text-xs text-foreground">{run.id}</div>
                      <div className="text-xs text-muted-foreground">{run.status}</div>
                    </div>
                    <div className="text-xs text-muted-foreground">{new Date(run.started_at).toLocaleString()}</div>
                  </button>
                ))}
              </div>
            )}
          </div>
        </DataPage.Group>
      </DataPage.Content>

    </DataPage>
  )
}
