// notebooks feature — K8s notebook form component
import { useEffect, useRef, useState } from 'react'
import {
  DataBodyTemplate, FormActions, FormField, PageTopBar,
  Select, SelectTrigger, SelectContent, SelectItem, SelectValue,
  Tabs, TabsList, TabsTrigger,
} from '@loykin/designkit'
import { Input } from '@/components/ui/input'
import { ShellMirror } from '@/components/ui/shell-mirror'
import { YamlMirror } from '@/components/ui/yaml-mirror'
import { EnvVarEditor } from '@/shared/components/EnvVarEditor'
import { emptyEnvVarDraft, type EnvVarDraft } from '@/shared/env'
import { useSystemSettings } from '@/features/system/hooks'
import type { NotebookVolume } from '../types'
import {
  buildK8sYAML, buildWorkerYAML, buildWorkerYAMLWithBackend,
  DEFAULT_K8S, DEFAULT_WORKER,
  type K8sFormState, type WorkerFormState,
} from '../editor'

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
    <FormField
      label="Volume"
      htmlFor="notebook-volume"
      helperText={selectedVol ? selectedVol.work_dir : 'Attach to a released volume to recover existing data, or leave blank to provision a new one.'}
    >
      <Select
        items={[
          { value: '', label: 'new volume' },
          ...releasedVolumes.map(v => ({
            value: v.id,
            label: <>{v.label}&nbsp;·&nbsp;<span className="font-mono text-xs">{v.id.slice(0, 8)}</span>{v.work_dir ? `  ${v.work_dir}` : ''}</>,
          })),
        ]}
        value={volumeId}
        onValueChange={v => onChange(v ?? '')}
      >
        <SelectTrigger id="notebook-volume" size="sm" className="h-8 text-sm"><SelectValue placeholder="— new volume —" /></SelectTrigger>
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
    </FormField>
  )
}

// ─── K8sFieldsSection ───────────────────────────────────────────────────────

interface K8sFieldsSectionProps {
  k8sForm: K8sFormState
  setK8sField: <K extends keyof K8sFormState>(key: K, value: K8sFormState[K]) => void
  volumeId: string
  releasedVolumes: NotebookVolume[]
  onVolumeChange: (id: string) => void
  onAddEnv: () => void
  onRemoveEnv: (rowIndex: number) => void
  onUpdateEnv: (rowIndex: number, patch: Partial<EnvVarDraft>) => void
}

function K8sFieldsSection({
  k8sForm, setK8sField,
  volumeId, releasedVolumes, onVolumeChange,
  onAddEnv, onRemoveEnv, onUpdateEnv,
}: K8sFieldsSectionProps) {
  return (
    <DataBodyTemplate.Group layout="stacked" title="Server">
      <FormField label="Server Name" htmlFor="k8s-name">
        <Input id="k8s-name" className="h-8 text-sm" value={k8sForm.name} onChange={e => setK8sField('name', e.target.value)} placeholder="my-notebook" autoFocus />
      </FormField>
      <VolumeField volumeId={volumeId} releasedVolumes={releasedVolumes} onChange={onVolumeChange} />
      <FormField label="Image" htmlFor="k8s-image" helperText="Required container image for the notebook server.">
        <Input id="k8s-image" className="h-8 text-sm" value={k8sForm.image} onChange={e => setK8sField('image', e.target.value)} placeholder="jupyter/scipy-notebook:latest" />
      </FormField>
      <FormField label="Namespace" htmlFor="k8s-namespace" helperText="Required. Kubernetes namespace where the notebook and its volume will be created.">
        <Input id="k8s-namespace" className="h-8 text-sm" value={k8sForm.namespace} onChange={e => setK8sField('namespace', e.target.value)} placeholder="notebooks" />
      </FormField>
      <FormField label="Storage Size" htmlFor="k8s-storage-size" helperText="Required PVC size. Defaults to 10Gi.">
        <Input id="k8s-storage-size" className="h-8 text-sm" value={k8sForm.storageSize} onChange={e => setK8sField('storageSize', e.target.value)} placeholder="10Gi" />
      </FormField>
      <FormField label="Prepare Commands" htmlFor="k8s-prepare" helperText="One command per line. Runs before notebook start.">
        <ShellMirror
          value={k8sForm.prepare}
          onChange={e => setK8sField('prepare', e.target.value)}
          minHeight="7rem"
          placeholder={`pip install -r requirements.txt\npython /work/preflight.py`}
        />
      </FormField>
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
        <FormField label="CPU" htmlFor="k8s-cpu">
          <Input id="k8s-cpu" className="h-8 text-sm" value={k8sForm.cpu} onChange={e => setK8sField('cpu', e.target.value)} placeholder="2" />
        </FormField>
        <FormField label="Memory" htmlFor="k8s-memory">
          <Input id="k8s-memory" className="h-8 text-sm" value={k8sForm.memory} onChange={e => setK8sField('memory', e.target.value)} placeholder="4Gi" />
        </FormField>
        <FormField label="GPU" htmlFor="k8s-gpu">
          <Input id="k8s-gpu" className="h-8 text-sm" value={k8sForm.gpu} onChange={e => setK8sField('gpu', e.target.value)} placeholder="1" />
        </FormField>
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
      <FormField label="Server Name" htmlFor="worker-name">
        <Input id="worker-name" className="h-8 text-sm" value={workerForm.name} onChange={e => setWorkerField('name', e.target.value)} placeholder="my-notebook" autoFocus />
      </FormField>
      <VolumeField volumeId={volumeId} releasedVolumes={releasedVolumes} onChange={onVolumeChange} />
      {runtime === 'docker' ? (
        <FormField label="Image" htmlFor="worker-image" helperText="Container image used to run the notebook server.">
          <Input id="worker-image" className="h-8 text-sm" value={workerForm.dockerImage} onChange={e => setWorkerField('dockerImage', e.target.value)} placeholder="jupyter/minimal-notebook:latest" />
        </FormField>
      ) : (
        <FormField
          label="Python Environment"
          htmlFor="worker-env"
          helperText="venv path (e.g. /project/venv) or conda env (e.g. conda:ml-env). Leave blank to auto-create a .venv."
        >
          <Input id="worker-env" className="h-8 text-sm" value={workerForm.env} onChange={e => setWorkerField('env', e.target.value)} placeholder="/home/user/project/venv" />
        </FormField>
      )}
      <FormField label="GPUs" htmlFor="worker-gpus" helperText="Device IDs: 0 · 0,1 · all · leave blank for no GPU">
        <Input id="worker-gpus" className="h-8 text-sm" value={workerForm.gpus} onChange={e => setWorkerField('gpus', e.target.value)} placeholder="0" />
      </FormField>
      <FormField label="Prepare Commands" htmlFor="worker-prepare" helperText="One command per line. Runs before notebook start.">
        <ShellMirror
          value={workerForm.prepare}
          onChange={e => setWorkerField('prepare', e.target.value)}
          minHeight="7rem"
          placeholder={`uv pip install jupyterlab ipykernel\npython -m ipykernel install --sys-prefix`}
        />
      </FormField>
      <EnvVarEditor items={workerForm.envVars} onAdd={onAddEnv} onRemove={onRemoveEnv} onUpdate={onUpdateEnv} />
    </DataBodyTemplate.Group>
  )
}

interface NotebookK8sFormProps {
  releasedVolumes: NotebookVolume[]
  preselectedVolume?: string
  onSubmit: (yaml: string, volumeId?: string) => void
  submitting: boolean
  error?: string
  onCancel: () => void
}

export function NotebookK8sForm({
  releasedVolumes, preselectedVolume = '',
  onSubmit, submitting, error, onCancel,
}: NotebookK8sFormProps) {
  const [tab, setTab] = useState('form')

  const [k8sForm, setK8sForm] = useState<K8sFormState>(DEFAULT_K8S)
  const [k8sYaml, setK8sYaml] = useState(() => buildK8sYAML(DEFAULT_K8S))

  const [workerForm, setWorkerForm] = useState<WorkerFormState>(DEFAULT_WORKER)
  const [workerYaml, setWorkerYaml] = useState(() => buildWorkerYAML(DEFAULT_WORKER))

  const [volumeId, setVolumeId] = useState(preselectedVolume)

  // This Piper installation owns exactly one runtime (baremetal, docker, or
  // k8s) for direct in-process execution — the notebook always launches on it.
  const { data: systemSettings } = useSystemSettings()
  const runtime = (systemSettings?.runtime?.type as 'k8s' | 'docker' | 'baremetal' | undefined) || 'baremetal'

  const workerPrepareBackend = runtime === 'docker' ? 'docker' : 'process'

  const prepareBackendSyncedRef = useRef<'process' | 'docker' | null>(null)
  useEffect(() => {
    if (prepareBackendSyncedRef.current === workerPrepareBackend) return
    prepareBackendSyncedRef.current = workerPrepareBackend
    setWorkerForm(prev => (prev.prepareBackend === workerPrepareBackend ? prev : { ...prev, prepareBackend: workerPrepareBackend }))
  }, [workerPrepareBackend])

  function setK8sField<K extends keyof K8sFormState>(key: K, value: K8sFormState[K]) {
    setK8sForm(prev => {
      const next = { ...prev, [key]: value }
      setK8sYaml(buildK8sYAML(next))
      return next
    })
  }

  function setWorkerField<K extends keyof WorkerFormState>(key: K, value: WorkerFormState[K]) {
    setWorkerForm(prev => {
      const next = { ...prev, [key]: value }
      setWorkerYaml(buildWorkerYAMLWithBackend(next, workerPrepareBackend))
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

  function handleTabChange(nextTab: string) {
    if (tab === 'form' && nextTab === 'yaml') {
      setK8sYaml(buildK8sYAML(k8sForm))
      setWorkerYaml(buildWorkerYAMLWithBackend(workerForm, workerPrepareBackend))
    }
    setTab(nextTab)
  }

  function handleSubmit() {
    const isK8s = runtime === 'k8s'
    const name = isK8s ? k8sForm.name : workerForm.name
    const formReady = isK8s
      ? Boolean(name.trim() && k8sForm.image.trim() && k8sForm.namespace.trim() && k8sForm.storageSize.trim())
      : Boolean(name.trim() && (runtime !== 'docker' || workerForm.dockerImage.trim()))
    if (tab === 'form' && !formReady) return
    const payload = tab === 'form'
      ? (isK8s
        ? buildK8sYAML(k8sForm)
        : buildWorkerYAMLWithBackend(workerForm, workerPrepareBackend))
      : (isK8s ? k8sYaml : workerYaml)
    if (!payload.trim()) return
    onSubmit(payload.trim(), volumeId || undefined)
  }

  const submitDisabled = submitting || (tab === 'form' && (
    runtime === 'k8s'
      ? !k8sForm.name.trim() || !k8sForm.image.trim() || !k8sForm.namespace.trim() || !k8sForm.storageSize.trim()
      : !workerForm.name.trim() || (runtime === 'docker' && !workerForm.dockerImage.trim())
  ))

  return (
    <DataBodyTemplate
      topBar={<PageTopBar left="Notebooks / Launch" />}
      title="Launch Notebook Server"
      description={<RuntimeBadge runtime={runtime} />}
    >
      <Tabs value={tab} onValueChange={value => handleTabChange(value ?? 'form')}>
        <TabsList>
          <TabsTrigger value="form">Form</TabsTrigger>
          <TabsTrigger value="yaml">YAML</TabsTrigger>
        </TabsList>
      </Tabs>

      <form
        className="contents"
        onSubmit={e => {
          e.preventDefault()
          handleSubmit()
        }}
      >
        {tab === 'form' ? (
          <>
            {runtime === 'k8s' ? (
              <>
                <K8sFieldsSection
                  k8sForm={k8sForm}
                  setK8sField={setK8sField}
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

        <FormActions
          status={error || undefined}
          submitLabel={submitting ? 'Launching…' : volumeId ? 'Attach & Launch' : 'Launch'}
          submitDisabled={submitDisabled}
          onCancel={onCancel}
        />
      </form>
    </DataBodyTemplate>
  )
}
