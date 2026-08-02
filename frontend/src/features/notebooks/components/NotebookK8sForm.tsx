// notebooks feature — K8s notebook form component
import { useMemo, useState } from 'react'
import {
  DataBodyTemplate,
  Select, SelectTrigger, SelectContent, SelectItem, SelectValue,
} from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
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
    <DataBodyTemplate.Field label="Volume" description="Attach to a released volume to recover existing data, or leave blank to provision a new one.">
      <Select value={volumeId} onValueChange={v => onChange(v ?? '')}>
        <SelectTrigger size="sm"><SelectValue placeholder="— new volume —" /></SelectTrigger>
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
      {selectedVol && <p className="mt-1 font-mono text-xs text-muted-foreground">{selectedVol.work_dir}</p>}
    </DataBodyTemplate.Field>
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
    if (!hasWorkers) return
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

  return (
    <DataBodyTemplate
      title="Launch Notebook Server"
      description={ambiguousInfra ? undefined : <RuntimeBadge runtime={runtime} />}
      activeTab={tab}
      onTabChange={handleTabChange}
      actions={
        <div className="flex items-center gap-2">
          {error && <span className="text-sm text-destructive">{error}</span>}
          <Button variant="outline" size="sm" onClick={onCancel}>Cancel</Button>
          <Button
            size="sm"
            onClick={handleSubmit}
            disabled={submitting || !hasWorkers || ambiguousInfra || (tab === 'form' && (
              runtime === 'k8s'
                ? !k8sForm.name.trim() || !k8sForm.image.trim() || !resolveK8sForm(k8sForm).namespace.trim() || !k8sForm.storageSize.trim()
                : !workerForm.name.trim() || (runtime === 'docker' && !workerForm.dockerImage.trim())
            ))}
          >
            {submitting ? 'Launching…' : volumeId ? 'Attach & Launch' : 'Launch'}
          </Button>
        </div>
      }
    >
      <DataBodyTemplate.Tab id="form" label="Form">
        {!hasWorkers && (
          <DataBodyTemplate.Group layout="stacked">
            <p className="rounded border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              No worker in this project advertises the notebook capability. Launching would fail immediately — register a worker with notebook support first.
            </p>
          </DataBodyTemplate.Group>
        )}
        {ambiguousInfra && (
          <DataBodyTemplate.Group layout="stacked">
            <p className="rounded border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {notebookInfraTypes.length} worker infrastructure types are registered ({notebookInfraTypes.join(', ')}) — choose a Worker so this notebook always runs where you expect.
            </p>
          </DataBodyTemplate.Group>
        )}
        <DataBodyTemplate.Group layout="stacked">
          <DataBodyTemplate.Field
            label="Worker"
            description={
              notebookInfraTypes.length > 1
                ? 'Multiple infrastructure types are registered — pick the worker to run on.'
                : 'Optional. Leave unassigned to load-balance across matching workers.'
            }
          >
            <Select value={selectedWorkerID} onValueChange={onWorkerChange}>
              <SelectTrigger size="sm"><SelectValue placeholder="— select worker —" /></SelectTrigger>
              <SelectContent>
                {notebookInfraTypes.length <= 1 && <SelectItem value="">auto assign</SelectItem>}
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
          </DataBodyTemplate.Field>
        </DataBodyTemplate.Group>

        {runtime === 'k8s' && (
          <>
            <DataBodyTemplate.Group layout="stacked">
              <DataBodyTemplate.Field label="Server Name">
                <Input value={k8sForm.name} onChange={e => setK8sField('name', e.target.value)} placeholder="my-notebook" autoFocus />
              </DataBodyTemplate.Field>
              <VolumeField volumeId={volumeId} releasedVolumes={releasedVolumes} onChange={setVolumeId} />
              <DataBodyTemplate.Field label="Image" description="Required container image for the notebook server.">
                <Input value={k8sForm.image} onChange={e => setK8sField('image', e.target.value)} placeholder="jupyter/scipy-notebook:latest" />
              </DataBodyTemplate.Field>
              <DataBodyTemplate.Field label="Namespace" description="Kubernetes namespace where the notebook and its volume will be created.">
                <Input value={k8sForm.namespace || defaultK8sNamespace} onChange={e => setK8sField('namespace', e.target.value)} placeholder="notebooks" />
              </DataBodyTemplate.Field>
              <DataBodyTemplate.Field label="Storage Size" description="Required PVC size. Defaults to 10Gi.">
                <Input value={k8sForm.storageSize} onChange={e => setK8sField('storageSize', e.target.value)} placeholder="10Gi" />
              </DataBodyTemplate.Field>
              <DataBodyTemplate.Field label="Prepare Commands" description="One command per line. Runs before notebook start.">
                <ShellMirror
                  value={k8sForm.prepare}
                  onChange={e => setK8sField('prepare', e.target.value)}
                  minHeight="7rem"
                  placeholder={`pip install -r requirements.txt\npython /work/preflight.py`}
                />
              </DataBodyTemplate.Field>
              <EnvVarEditor
                items={k8sForm.env}
                onAdd={addK8sEnv}
                onRemove={removeK8sEnv}
                onUpdate={updateK8sEnv}
              />
            </DataBodyTemplate.Group>
            <DataBodyTemplate.Group layout="stacked" variant="bordered" title="Resources" description="Optional CPU, memory, and GPU requests/limits.">
              <div className="grid grid-cols-3 gap-3">
                <DataBodyTemplate.Field label="CPU">
                  <Input value={k8sForm.cpu} onChange={e => setK8sField('cpu', e.target.value)} placeholder="2" />
                </DataBodyTemplate.Field>
                <DataBodyTemplate.Field label="Memory">
                  <Input value={k8sForm.memory} onChange={e => setK8sField('memory', e.target.value)} placeholder="4Gi" />
                </DataBodyTemplate.Field>
                <DataBodyTemplate.Field label="GPU">
                  <Input value={k8sForm.gpu} onChange={e => setK8sField('gpu', e.target.value)} placeholder="1" />
                </DataBodyTemplate.Field>
              </div>
            </DataBodyTemplate.Group>
          </>
        )}

        {runtime !== 'k8s' && (
          <DataBodyTemplate.Group layout="stacked">
            <DataBodyTemplate.Field label="Server Name">
              <Input value={workerForm.name} onChange={e => setWorkerField('name', e.target.value)} placeholder="my-notebook" autoFocus />
            </DataBodyTemplate.Field>
            <VolumeField volumeId={volumeId} releasedVolumes={releasedVolumes} onChange={setVolumeId} />
            {runtime === 'docker' ? (
              <DataBodyTemplate.Field label="Image" description="Container image used to run the notebook server.">
                <Input value={workerForm.dockerImage} onChange={e => setWorkerField('dockerImage', e.target.value)} placeholder="jupyter/minimal-notebook:latest" />
              </DataBodyTemplate.Field>
            ) : (
              <DataBodyTemplate.Field label="Python Environment" description="venv path (e.g. /project/venv) or conda env (e.g. conda:ml-env). Leave blank to use the worker default.">
                <Input value={workerForm.env} onChange={e => setWorkerField('env', e.target.value)} placeholder="/home/user/project/venv" />
              </DataBodyTemplate.Field>
            )}
            <DataBodyTemplate.Field label="GPUs" description="Device IDs: 0 · 0,1 · all · leave blank for no GPU">
              <Input value={workerForm.gpus} onChange={e => setWorkerField('gpus', e.target.value)} placeholder="0" />
            </DataBodyTemplate.Field>
            <DataBodyTemplate.Field label="Prepare Commands" description="One command per line. Runs before notebook start.">
              <ShellMirror
                value={workerForm.prepare}
                onChange={e => setWorkerField('prepare', e.target.value)}
                minHeight="7rem"
                placeholder={`uv pip install jupyterlab ipykernel\npython -m ipykernel install --sys-prefix`}
              />
            </DataBodyTemplate.Field>
            <EnvVarEditor
              items={workerForm.envVars}
              onAdd={addWorkerEnv}
              onRemove={removeWorkerEnv}
              onUpdate={updateWorkerEnv}
            />
          </DataBodyTemplate.Group>
        )}
      </DataBodyTemplate.Tab>

      <DataBodyTemplate.Tab id="yaml" label="YAML">
        <DataBodyTemplate.Group layout="stacked">
          <YamlMirror
            rows={24}
            value={runtime === 'k8s' ? k8sYaml : workerYaml}
            onChange={e => runtime === 'k8s' ? setK8sYaml(e.target.value) : setWorkerYaml(e.target.value)}
          />
        </DataBodyTemplate.Group>
      </DataBodyTemplate.Tab>
    </DataBodyTemplate>
  )
}
