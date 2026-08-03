// notebooks feature — K8s notebook form component
import { useMemo, useState } from 'react'
import {
  DataBodyTemplate, PageTopBar,
  Select, SelectTrigger, SelectContent, SelectItem, SelectValue,
  Tabs, TabsList, TabsTrigger,
} from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ShellMirror } from '@/components/ui/shell-mirror'
import { YamlMirror } from '@/components/ui/yaml-mirror'
import { EnvVarEditor } from '@/shared/components/EnvVarEditor'
import { emptyEnvVarDraft, type EnvVarDraft } from '@/shared/env'
import type { NotebookVolume, NotebookWorkerInfo } from '../types'
import {
  buildK8sYAML, buildWorkerYAML, buildWorkerYAMLWithBackend,
  DEFAULT_K8S, DEFAULT_WORKER,
  type K8sFormState, type WorkerFormState,
} from '../editor'

function workerLabel(w: NotebookWorkerInfo): string {
	const base = w.infrastructure === 'k8s' ? (w.cluster_name || w.hostname || w.id) : (w.hostname || w.id)
	return base
}

function RuntimeBadge({ runtime }: { runtime: 'k8s' | 'docker' | 'baremetal' }) {
  return (
    <span className={`rounded px-2 py-0.5 text-xs font-medium ${
      runtime === 'k8s' ? 'bg-blue-500/15 text-blue-400' : runtime === 'docker' ? 'bg-cyan-500/15 text-cyan-400' : 'bg-orange-500/15 text-orange-400'
    }`}>
      {runtime === 'k8s' ? 'Kubernetes' : runtime === 'docker' ? 'Docker' : 'Bare-metal'}
    </span>
  )
}

interface VolumeFieldProps {
  volumeId: string
  releasedVolumes: NotebookVolume[]
  onChange: (id: string) => void
}

function VolumeField({ volumeId, releasedVolumes, onChange }: VolumeFieldProps) {
  const selectedVol = releasedVolumes.find(v => v.id === volumeId)
  return (
    <div className="space-y-1.5">
      <Label className="text-xs">Volume</Label>
      <p className="text-xs text-muted-foreground">Attach to a released volume to recover existing data, or leave blank to provision a new one.</p>
      <Select value={volumeId} onValueChange={v => onChange(v ?? '')}>
        <SelectTrigger size="sm" className="h-8 text-sm"><SelectValue placeholder="— new volume —" /></SelectTrigger>
        <SelectContent>
          <SelectItem value="">new volume</SelectItem>
          {releasedVolumes.map(v => (
            <SelectItem key={v.id} value={v.id}>
              {v.label}&nbsp;·&nbsp;<span className="font-mono text-xs">{v.id.slice(0, 8)}</span>
              {v.work_dir ? `  ${v.work_dir}` : ''}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {selectedVol && <p className="font-mono text-xs text-muted-foreground">{selectedVol.work_dir}</p>}
    </div>
  )
}

// ─── WorkerSelectSection ────────────────────────────────────────────────────

interface WorkerSelectSectionProps {
  workers: NotebookWorkerInfo[]
  notebookInfraTypes: string[]
  selectedWorkerID: string
  onWorkerChange: (id: string | null) => void
}

function WorkerSelectSection({ workers, notebookInfraTypes, selectedWorkerID, onWorkerChange }: WorkerSelectSectionProps) {
  return (
    <DataBodyTemplate.Group layout="stacked" title="Worker">
      <div className="space-y-1.5">
        <Label className="text-xs">Worker</Label>
        <p className="text-xs text-muted-foreground">
          {notebookInfraTypes.length > 1
            ? 'Multiple infrastructure types are registered — pick the worker to run on.'
            : 'Pick the worker to run on. Separately managed workers of the same type are never chosen automatically.'}
        </p>
        <Select value={selectedWorkerID} onValueChange={onWorkerChange}>
          <SelectTrigger size="sm" className="h-8 text-sm" aria-invalid={!selectedWorkerID}><SelectValue placeholder="— select worker —" /></SelectTrigger>
          <SelectContent>
            {workers.map(w => (
              <SelectItem key={w.id} value={w.id}>
                <span className={`mr-1.5 rounded px-1 py-0.5 text-[10px] font-medium ${
                  w.infrastructure === 'k8s' ? 'bg-blue-500/15 text-blue-400' : w.infrastructure === 'docker' ? 'bg-cyan-500/15 text-cyan-400' : 'bg-orange-500/15 text-orange-400'
                }`}>
                  {w.infrastructure === 'k8s' ? 'K8s' : w.infrastructure === 'docker' ? 'Docker' : 'BM'}
                </span>
                {workerLabel(w)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
    </DataBodyTemplate.Group>
  )
}

// ─── K8sFieldsSection ───────────────────────────────────────────────────────

interface K8sFieldsSectionProps {
  k8sForm: K8sFormState
  setK8sField: <K extends keyof K8sFormState>(key: K, value: K8sFormState[K]) => void
  defaultK8sNamespace: string
  volumeId: string
  releasedVolumes: NotebookVolume[]
  onVolumeChange: (id: string) => void
  onAddEnv: () => void
  onRemoveEnv: (rowIndex: number) => void
  onUpdateEnv: (rowIndex: number, patch: Partial<EnvVarDraft>) => void
}

function K8sFieldsSection({
  k8sForm, setK8sField, defaultK8sNamespace,
  volumeId, releasedVolumes, onVolumeChange,
  onAddEnv, onRemoveEnv, onUpdateEnv,
}: K8sFieldsSectionProps) {
  return (
    <DataBodyTemplate.Group layout="stacked" title="Server">
      <div className="space-y-1.5">
        <Label className="text-xs">Server Name</Label>
        <Input className="h-8 text-sm" value={k8sForm.name} onChange={e => setK8sField('name', e.target.value)} placeholder="my-notebook" autoFocus />
      </div>
      <VolumeField volumeId={volumeId} releasedVolumes={releasedVolumes} onChange={onVolumeChange} />
      <div className="space-y-1.5">
        <Label className="text-xs">Image</Label>
        <p className="text-xs text-muted-foreground">Required container image for the notebook server.</p>
        <Input className="h-8 text-sm" value={k8sForm.image} onChange={e => setK8sField('image', e.target.value)} placeholder="jupyter/scipy-notebook:latest" />
      </div>
      <div className="space-y-1.5">
        <Label className="text-xs">Namespace</Label>
        <p className="text-xs text-muted-foreground">Kubernetes namespace where the notebook and its volume will be created.</p>
        <Input className="h-8 text-sm" value={k8sForm.namespace || defaultK8sNamespace} onChange={e => setK8sField('namespace', e.target.value)} placeholder="notebooks" />
      </div>
      <div className="space-y-1.5">
        <Label className="text-xs">Storage Size</Label>
        <p className="text-xs text-muted-foreground">Required PVC size. Defaults to 10Gi.</p>
        <Input className="h-8 text-sm" value={k8sForm.storageSize} onChange={e => setK8sField('storageSize', e.target.value)} placeholder="10Gi" />
      </div>
      <div className="space-y-1.5">
        <Label className="text-xs">Prepare Commands</Label>
        <p className="text-xs text-muted-foreground">One command per line. Runs before notebook start.</p>
        <ShellMirror
          value={k8sForm.prepare}
          onChange={e => setK8sField('prepare', e.target.value)}
          minHeight="7rem"
          placeholder={`pip install -r requirements.txt\npython /work/preflight.py`}
        />
      </div>
      <EnvVarEditor items={k8sForm.env} onAdd={onAddEnv} onRemove={onRemoveEnv} onUpdate={onUpdateEnv} />
    </DataBodyTemplate.Group>
  )
}

// ─── ResourcesSection ───────────────────────────────────────────────────────

interface ResourcesSectionProps {
  k8sForm: K8sFormState
  setK8sField: <K extends keyof K8sFormState>(key: K, value: K8sFormState[K]) => void
}

function ResourcesSection({ k8sForm, setK8sField }: ResourcesSectionProps) {
  return (
    <DataBodyTemplate.Group layout="stacked" title="Resources" description="Optional CPU, memory, and GPU requests/limits.">
      <div className="grid grid-cols-3 gap-3">
        <div className="space-y-1.5">
          <Label className="text-xs">CPU</Label>
          <Input className="h-8 text-sm" value={k8sForm.cpu} onChange={e => setK8sField('cpu', e.target.value)} placeholder="2" />
        </div>
        <div className="space-y-1.5">
          <Label className="text-xs">Memory</Label>
          <Input className="h-8 text-sm" value={k8sForm.memory} onChange={e => setK8sField('memory', e.target.value)} placeholder="4Gi" />
        </div>
        <div className="space-y-1.5">
          <Label className="text-xs">GPU</Label>
          <Input className="h-8 text-sm" value={k8sForm.gpu} onChange={e => setK8sField('gpu', e.target.value)} placeholder="1" />
        </div>
      </div>
    </DataBodyTemplate.Group>
  )
}

// ─── WorkerFieldsSection ────────────────────────────────────────────────────

interface WorkerFieldsSectionProps {
  workerForm: WorkerFormState
  runtime: 'k8s' | 'docker' | 'baremetal'
  setWorkerField: <K extends keyof WorkerFormState>(key: K, value: WorkerFormState[K]) => void
  volumeId: string
  releasedVolumes: NotebookVolume[]
  onVolumeChange: (id: string) => void
  onAddEnv: () => void
  onRemoveEnv: (rowIndex: number) => void
  onUpdateEnv: (rowIndex: number, patch: Partial<EnvVarDraft>) => void
}

function WorkerFieldsSection({
  workerForm, runtime, setWorkerField,
  volumeId, releasedVolumes, onVolumeChange,
  onAddEnv, onRemoveEnv, onUpdateEnv,
}: WorkerFieldsSectionProps) {
  return (
    <DataBodyTemplate.Group layout="stacked" title="Server">
      <div className="space-y-1.5">
        <Label className="text-xs">Server Name</Label>
        <Input className="h-8 text-sm" value={workerForm.name} onChange={e => setWorkerField('name', e.target.value)} placeholder="my-notebook" autoFocus />
      </div>
      <VolumeField volumeId={volumeId} releasedVolumes={releasedVolumes} onChange={onVolumeChange} />
      {runtime === 'docker' ? (
        <div className="space-y-1.5">
          <Label className="text-xs">Image</Label>
          <p className="text-xs text-muted-foreground">Container image used to run the notebook server.</p>
          <Input className="h-8 text-sm" value={workerForm.dockerImage} onChange={e => setWorkerField('dockerImage', e.target.value)} placeholder="jupyter/minimal-notebook:latest" />
        </div>
      ) : (
        <div className="space-y-1.5">
          <Label className="text-xs">Python Environment</Label>
          <p className="text-xs text-muted-foreground">venv path (e.g. /project/venv) or conda env (e.g. conda:ml-env). Leave blank to use the worker default.</p>
          <Input className="h-8 text-sm" value={workerForm.env} onChange={e => setWorkerField('env', e.target.value)} placeholder="/home/user/project/venv" />
        </div>
      )}
      <div className="space-y-1.5">
        <Label className="text-xs">GPUs</Label>
        <p className="text-xs text-muted-foreground">Device IDs: 0 · 0,1 · all · leave blank for no GPU</p>
        <Input className="h-8 text-sm" value={workerForm.gpus} onChange={e => setWorkerField('gpus', e.target.value)} placeholder="0" />
      </div>
      <div className="space-y-1.5">
        <Label className="text-xs">Prepare Commands</Label>
        <p className="text-xs text-muted-foreground">One command per line. Runs before notebook start.</p>
        <ShellMirror
          value={workerForm.prepare}
          onChange={e => setWorkerField('prepare', e.target.value)}
          minHeight="7rem"
          placeholder={`uv pip install jupyterlab ipykernel\npython -m ipykernel install --sys-prefix`}
        />
      </div>
      <EnvVarEditor items={workerForm.envVars} onAdd={onAddEnv} onRemove={onRemoveEnv} onUpdate={onUpdateEnv} />
    </DataBodyTemplate.Group>
  )
}

interface NotebookK8sFormProps {
  workers: NotebookWorkerInfo[]
  releasedVolumes: NotebookVolume[]
  preselectedVolume?: string
  onSubmit: (yaml: string, volumeId?: string) => void
  submitting: boolean
  error?: string
  onCancel: () => void
}

export function NotebookK8sForm({
  workers, releasedVolumes, preselectedVolume = '',
  onSubmit, submitting, error, onCancel,
}: NotebookK8sFormProps) {
  const [selectedWorkerID, setSelectedWorkerID] = useState('')
  const [tab, setTab] = useState('form')

  const [k8sForm, setK8sForm] = useState<K8sFormState>(DEFAULT_K8S)
  const [k8sYaml, setK8sYaml] = useState(() => buildK8sYAML(DEFAULT_K8S))

  const [workerForm, setWorkerForm] = useState<WorkerFormState>(DEFAULT_WORKER)
  const [workerYaml, setWorkerYaml] = useState(() => buildWorkerYAML(DEFAULT_WORKER))

  const [volumeId, setVolumeId] = useState(preselectedVolume)

  const selectedWorker = useMemo(
    () => workers.find(w => w.id === selectedWorkerID) ?? null,
    [workers, selectedWorkerID],
  )

  const hasWorkers = workers.length > 0

  const notebookInfraTypes = useMemo(
    () => [...new Set(workers.map(w => w.infrastructure))],
    [workers],
  )
  const ambiguousInfra = notebookInfraTypes.length > 1 && !selectedWorkerID

  const runtime = useMemo<'k8s' | 'docker' | 'baremetal'>(() => {
		if (selectedWorker) return selectedWorker.infrastructure
		if (workers.some(w => w.infrastructure === 'baremetal')) return 'baremetal'
		if (workers.some(w => w.infrastructure === 'docker')) return 'docker'
		if (workers.some(w => w.infrastructure === 'k8s')) return 'k8s'
    return 'baremetal'
  }, [selectedWorker, workers])

  const defaultK8sNamespace = useMemo(
    () => (selectedWorker?.infrastructure === 'k8s'
      ? selectedWorker
      : workers.find(w => w.infrastructure === 'k8s'))?.namespaces?.[0] ?? '',
    [selectedWorker, workers],
  )

  function resolveK8sForm(form: K8sFormState): K8sFormState {
    return form.namespace ? form : { ...form, namespace: defaultK8sNamespace }
  }

  const workerPrepareBackend = useMemo<'process' | 'docker'>(() => {
    return runtime === 'docker' ? 'docker' : 'process'
  }, [runtime])

  function setK8sField<K extends keyof K8sFormState>(key: K, value: K8sFormState[K]) {
    setK8sForm(prev => {
      const next = { ...prev, [key]: value }
      setK8sYaml(buildK8sYAML(resolveK8sForm(next), selectedWorker?.id))
      return next
    })
  }

  function setWorkerField<K extends keyof WorkerFormState>(key: K, value: WorkerFormState[K]) {
    setWorkerForm(prev => {
      const next = { ...prev, [key]: value }
      const backend = runtime === 'docker' ? 'docker' : 'process'
      setWorkerYaml(buildWorkerYAMLWithBackend(next, selectedWorker?.id, backend))
      return next
    })
  }

  function addK8sEnv() {
    setK8sField('env', [...k8sForm.env, emptyEnvVarDraft()])
  }

  function updateK8sEnv(rowIndex: number, patch: Partial<EnvVarDraft>) {
    setK8sField('env', k8sForm.env.map((item, i) => i === rowIndex ? { ...item, ...patch } : item))
  }

  function removeK8sEnv(rowIndex: number) {
    setK8sField('env', k8sForm.env.filter((_, i) => i !== rowIndex))
  }

  function addWorkerEnv() {
    setWorkerField('envVars', [...workerForm.envVars, emptyEnvVarDraft()])
  }

  function updateWorkerEnv(rowIndex: number, patch: Partial<EnvVarDraft>) {
    setWorkerField('envVars', workerForm.envVars.map((item, i) => i === rowIndex ? { ...item, ...patch } : item))
  }

  function removeWorkerEnv(rowIndex: number) {
    setWorkerField('envVars', workerForm.envVars.filter((_, i) => i !== rowIndex))
  }

  function onWorkerChange(id: string | null) {
    setSelectedWorkerID(id ?? '')
    const w = workers.find(x => x.id === id) ?? null
    const nextK8sForm = w?.infrastructure === 'k8s' && !k8sForm.namespace
      ? { ...k8sForm, namespace: w.namespaces?.[0] ?? '' }
      : k8sForm
    setK8sForm(nextK8sForm)
    setK8sYaml(buildK8sYAML(nextK8sForm, w?.id))
		if (w?.infrastructure === 'baremetal' || w?.infrastructure === 'docker') {
		  const backend = w.infrastructure === 'docker' ? 'docker' : 'process'
      setWorkerForm(prev => ({ ...prev, prepareBackend: backend }))
      setWorkerYaml(buildWorkerYAMLWithBackend(workerForm, w?.id, backend))
    } else {
      setWorkerYaml(buildWorkerYAML(workerForm, w?.id))
    }
  }

  function handleTabChange(nextTab: string) {
    if (tab === 'form' && nextTab === 'yaml') {
      setK8sYaml(buildK8sYAML(resolveK8sForm(k8sForm), selectedWorker?.id))
      setWorkerYaml(buildWorkerYAMLWithBackend(workerForm, selectedWorker?.id, workerPrepareBackend))
    }
    setTab(nextTab)
  }

  function handleSubmit() {
    if (!hasWorkers || !selectedWorkerID) return
    const isK8s = runtime === 'k8s'
    const name = isK8s ? k8sForm.name : workerForm.name
    const resolvedK8s = resolveK8sForm(k8sForm)
    const formReady = isK8s
      ? Boolean(name.trim() && resolvedK8s.image.trim() && resolvedK8s.namespace.trim() && resolvedK8s.storageSize.trim())
      : Boolean(name.trim() && (runtime !== 'docker' || workerForm.dockerImage.trim()))
    if (tab === 'form' && !formReady) return
    const payload = tab === 'form'
      ? (isK8s
        ? buildK8sYAML(resolvedK8s, selectedWorker?.id)
        : buildWorkerYAMLWithBackend(workerForm, selectedWorker?.id, workerPrepareBackend))
      : (isK8s ? k8sYaml : workerYaml)
    if (!payload.trim()) return
    onSubmit(payload.trim(), volumeId || undefined)
  }

  const submitDisabled = submitting || !hasWorkers || ambiguousInfra || !selectedWorkerID || (tab === 'form' && (
    runtime === 'k8s'
      ? !k8sForm.name.trim() || !k8sForm.image.trim() || !resolveK8sForm(k8sForm).namespace.trim() || !k8sForm.storageSize.trim()
      : !workerForm.name.trim() || (runtime === 'docker' && !workerForm.dockerImage.trim())
  ))

  return (
    <DataBodyTemplate
      topBar={<PageTopBar left="Notebooks / Launch" />}
      title="Launch Notebook Server"
      description={ambiguousInfra ? undefined : <RuntimeBadge runtime={runtime} />}
    >
      <Tabs value={tab} onValueChange={value => handleTabChange(value ?? 'form')}>
        <TabsList>
          <TabsTrigger value="form">Form</TabsTrigger>
          <TabsTrigger value="yaml">YAML</TabsTrigger>
        </TabsList>
      </Tabs>

      {tab === 'form' ? (
        <>
          {!hasWorkers && (
            <p className="rounded border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              No worker in this project advertises the notebook capability. Launching would fail immediately — register a worker with notebook support first.
            </p>
          )}
          {ambiguousInfra && (
            <p className="rounded border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {notebookInfraTypes.length} worker infrastructure types are registered ({notebookInfraTypes.join(', ')}) — choose a Worker so this notebook always runs where you expect.
            </p>
          )}

          <WorkerSelectSection
            workers={workers}
            notebookInfraTypes={notebookInfraTypes}
            selectedWorkerID={selectedWorkerID}
            onWorkerChange={onWorkerChange}
          />

          {runtime === 'k8s' ? (
            <>
              <K8sFieldsSection
                k8sForm={k8sForm}
                setK8sField={setK8sField}
                defaultK8sNamespace={defaultK8sNamespace}
                volumeId={volumeId}
                releasedVolumes={releasedVolumes}
                onVolumeChange={setVolumeId}
                onAddEnv={addK8sEnv}
                onRemoveEnv={removeK8sEnv}
                onUpdateEnv={updateK8sEnv}
              />
              <ResourcesSection k8sForm={k8sForm} setK8sField={setK8sField} />
            </>
          ) : (
            <WorkerFieldsSection
              workerForm={workerForm}
              runtime={runtime}
              setWorkerField={setWorkerField}
              volumeId={volumeId}
              releasedVolumes={releasedVolumes}
              onVolumeChange={setVolumeId}
              onAddEnv={addWorkerEnv}
              onRemoveEnv={removeWorkerEnv}
              onUpdateEnv={updateWorkerEnv}
            />
          )}
        </>
      ) : (
        <DataBodyTemplate.Group layout="stacked" title="YAML">
          <YamlMirror
            rows={24}
            value={runtime === 'k8s' ? k8sYaml : workerYaml}
            onChange={e => runtime === 'k8s' ? setK8sYaml(e.target.value) : setWorkerYaml(e.target.value)}
          />
        </DataBodyTemplate.Group>
      )}

      {error && <p className="text-sm text-destructive" role="alert">{error}</p>}

      <div className="flex justify-end gap-2 border-t border-border pt-(--designkit-panel-gap)">
        <Button type="button" variant="outline" size="sm" onClick={onCancel}>Cancel</Button>
        <Button size="sm" onClick={handleSubmit} disabled={submitDisabled}>
          {submitting ? 'Launching…' : volumeId ? 'Attach & Launch' : 'Launch'}
        </Button>
      </div>
    </DataBodyTemplate>
  )
}
