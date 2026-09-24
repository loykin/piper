import { useEffect, useMemo, useRef, useState, type DragEvent } from 'react'
import { useNavigate, useSearchParams } from '@/lib/router'
import { HardDrive, Upload } from 'lucide-react'
import {
  DataBodyTemplate, PageTopBar, WorkbenchBodyTemplate, Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
  Tabs, TabsContent, TabsList, TabsTrigger,
} from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { YamlMirror } from '@/components/ui/yaml-mirror'
import PipelineCanvas from '@/features/pipelines/components/PipelineCanvas'
import { useNotebookVolumes, useVolumeFiles } from '@/features/notebooks/hooks'
import { useCreatePipeline, usePipeline } from '@/features/pipelines/hooks'
import { useSystemSettings } from '@/features/system/hooks'
import { useProjectId } from '@/features/projects/context'
import {
  buildPipelineDraftYaml, defaultPipelineDraft,
  findPipelineDraftYamlDifference, parsePipelineDraftYaml, stripMetadataVersion,
  type PipelineSourceDraft, type PipelineDefaultsDraft, type PipelineTaskType,
} from '@/features/pipelines/editor'
import { toneText } from '@/shared/status'
import { errorMessage } from '@/lib/format'
import { PageCrumbs } from '@/shared/components/PageCrumbs'
import {
  RUNTIME_ITEMS, TASK_LABELS,
  buildPositions, designLossMessage, draftProblems,
  type ActiveTab, type SourceKind,
} from '@/features/pipelines/editorModel'
import { TaskIcon } from '@/features/pipelines/components/editor/TaskIcon'
import { PipelineSourceSetup } from '@/features/pipelines/components/editor/PipelineSourceSetup'
import { useTaskDraft } from '@/features/pipelines/useTaskDraft'
import { TaskEditorPane } from '@/features/pipelines/components/editor/TaskEditorPane'
import { SubmitTemplateDialog } from '@/features/pipelines/components/editor/SubmitTemplateDialog'
import { createCopy } from '@/lib/copy'


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

  const [activeTab, setActiveTab] = useState<ActiveTab>('design')
  const [pipelineName, setPipelineName] = useState(editorName)
  const { data: volumes = [] } = useNotebookVolumes()
  const volumeFilesQuery = useVolumeFiles(editorSourceKind === 'notebook-volume' ? editorVolumeId : '')
  const volumeFiles = volumeFilesQuery.data?.files ?? []
  const volumeFilesStatus = volumeFilesQuery.isError ? 'unavailable' : volumeFilesQuery.data?.state ?? null
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
  const draft = useTaskDraft(initialDraft.steps)
  const {
    tasks, positions, setPositions, selectedId, setSelectedId, editingId, setEditingId, replaceSteps,
    addTask, connectTasks, disconnectTasks,
  } = draft
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
  const pipelineSource = useMemo<PipelineSourceDraft | undefined>(() => {
    if (editorSourceKind !== 'git') return undefined
    return {
      type: 'git',
      repo: editorRepo,
      branch: editorBranch,
      credentialRef: editorCredential,
    }
  }, [editorSourceKind, editorRepo, editorBranch, editorCredential])

  // Seed the editor from ?from_version= once per version — a refetch (window
  // focus, invalidation) must not clobber edits made since.
  const fromTemplate = usePipeline(editorFromVersion)
  const seededFromRef = useRef('')
  useEffect(() => {
    const template = fromTemplate.data
    if (!template || seededFromRef.current === template.id) return
    seededFromRef.current = template.id
    // Preserve the fetched document before parsing. Even if the Design
    // parser cannot understand a valid server-side feature, the user can
    // still inspect, edit, and resubmit the exact stored YAML.
    // Strip the server-stamped metadata.version: submitting it unchanged
    // would collide with the version that already exists (ErrVersionExists)
    // instead of letting the server auto-assign the next one, the same way
    // a brand-new template submission does.
    try {
      const yaml = stripMetadataVersion(template.yaml)
      setPipelineName(template.name)
      setYamlText(yaml)
      setActiveTab('yaml')
      const parsed = parsePipelineDraftYaml(yaml)
      const designDraft = { ...parsed, source: pipelineSource }
      const difference = findPipelineDraftYamlDifference(yaml, designDraft)
      replaceSteps(parsed.steps)
      setDefaults(parsed.defaults)
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
    } catch (err) {
      setError(errorMessage(err))
    }
  }, [fromTemplate.data, pipelineSource, replaceSteps])
  useEffect(() => {
    if (fromTemplate.isError) setError(errorMessage(fromTemplate.error))
  }, [fromTemplate.isError, fromTemplate.error])


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
  }, [pipelineName, tasks, pipelineSource, defaults, selectedId, editingId, activeTab, setSelectedId, setEditingId])


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
      return { parseError: errorMessage(err), difference: null }
    }
  }, [pipelineSource, yamlText])


  function updateDefaults(patch: Partial<PipelineDefaultsDraft>) {
    setDefaults(current => ({ ...current, ...patch }))
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
      const messages = draftProblems(designDraft, serverRuntime)
      if (messages.length > 0) {
        setError(messages[0])
        return false
      }
      setPipelineName(designDraft.name)
      replaceSteps(designDraft.steps)
      setDefaults(designDraft.defaults)
      setYamlText(buildPipelineDraftYaml(designDraft))
      setError('')
      setActiveTab('design')
      return true
    } catch (err) {
      setError(errorMessage(err))
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
      const messages = draftProblems(draft, serverRuntime)
      if (messages.length > 0) {
        setError(messages[0])
        return null
      }
      return { name: pipelineName, yaml: buildPipelineDraftYaml(draft) }
    }

    let parsed
    try {
      parsed = parsePipelineDraftYaml(yamlText)
    } catch (err) {
      setError(errorMessage(err))
      return null
    }
    // Parsing alone only proves the YAML is well-formed — it skips the same
    // required-field checks the Design tab runs, so a required field left
    // empty in YAML used to pass through silently until the server rejected
    // it. Validate the parsed draft the same way, but still submit yamlText
    // verbatim (not a round-tripped rebuild) so the server's strict manifest
    // decoder sees exactly what the user typed.
    const messages = draftProblems(parsed, serverRuntime)
    if (messages.length > 0) {
      setError(messages[0])
      return null
    }
    return { name: parsed.name, yaml: yamlText }
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
      setError(errorMessage(err))
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
    setError(draftProblems({ name: pipelineName, steps: tasks, source: pipelineSource, defaults }, serverRuntime)[0] || '')
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










  if (!setupDone) {
    return (
      <DataBodyTemplate
        topBar={<PageTopBar left={<PageCrumbs items={['Pipelines', { label: 'Templates', to: `/projects/${projectId}/pipelines` }, createCopy('Template').crumb]} />} />}
        title={createCopy('Template').title}
        description="A pipeline uses exactly one source workspace. Lock it in before you start editing."
      >
        <DataBodyTemplate.Group layout="stacked">
          <PipelineSourceSetup
            initial={{
              name: editorName, sourceKind: editorSourceKind, volumeId: editorVolumeId, credential: editorCredential,
              repo: editorRepo, branch: editorBranch, root: editorRoot,
            }}
            volumes={volumes}
            onStart={(params, name) => {
              setPipelineName(name)
              setSearchParams(params, { replace: true })
            }}
            onCancel={() => navigate(`/projects/${projectId}/pipelines`)}
          />
        </DataBodyTemplate.Group>
      </DataBodyTemplate>
    )
  }

  return (
    <>
      <WorkbenchBodyTemplate
        topBar={<PageTopBar left={<PageCrumbs items={['Pipelines', { label: 'Templates', to: `/projects/${projectId}/pipelines` }, editorFromVersion ? 'New Version' : createCopy('Template').title]} />} />}
        title={editorFromVersion ? 'New Version' : createCopy('Template').title}
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
                    items={RUNTIME_ITEMS}
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
                      {<TaskIcon type={type} />}
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
            {error && (
              <p className="shrink-0 border-b border-border bg-destructive/5 px-4 py-2 text-sm text-destructive">{error}</p>
            )}

            <Tabs value={activeTab} onValueChange={value => handleTabChange(value as ActiveTab)} className="flex min-h-0 flex-1 flex-col">
              <TabsList variant="line" className="shrink-0 border-b border-border px-4">
                <TabsTrigger value="design">Design</TabsTrigger>
                <TabsTrigger value="yaml">YAML</TabsTrigger>
              </TabsList>

              <TabsContent value="design" className="relative min-h-0 flex-1 overflow-hidden p-3">
                <div className="absolute right-5 top-5 z-10">
                  <Button variant="outline" size="sm" onClick={resetLayout}>Reset Layout</Button>
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
                      <p className={`text-xs ${toneText.warning}`}>
                        YAML syntax is valid, but Design cannot preserve “{yamlStatus.difference}”. You can submit the original YAML from this tab.
                      </p>
                    ) : (
                      <p className={`text-xs ${toneText.success}`}>YAML is ready for Design. Server validation runs on submit.</p>
                    )}
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
          <TaskEditorPane
            key={editingTask.id}
            task={editingTask}
            index={editingIndex}
            taskCount={tasks.length}
            draft={draft}
            canBrowse={canBrowse}
            volumeFiles={volumeFiles}
            onClose={() => setEditingId(null)}
          />
        ) : undefined}
      />

      <SubmitTemplateDialog
        open={submitModalOpen}
        volumes={volumes}
        volumeId={submitVolumeId}
        onVolumeChange={setSubmitVolumeId}
        error={submitModalOpen ? error : undefined}
        submitting={submitting}
        onClose={closeSubmitModal}
        onConfirm={() => void confirmSubmit()}
      />
    </>
  )
}
