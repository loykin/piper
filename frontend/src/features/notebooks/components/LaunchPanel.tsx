import { useEffect, useState } from 'react'
import {
  DataBodyTemplate, DataPage,
  Select, SelectTrigger, SelectContent, SelectItem, SelectValue,
} from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  createNotebook, listNotebookWorkers, getNotebookMode,
  type NotebookDriverMode, type NotebookVolume, type NotebookWorkerInfo,
} from '../api'

// ── K8s form ──────────────────────────────────────────────────────────────────

interface K8sFormState {
  name: string
  image: string
  cpu: string
  memory: string
  gpu: string
  storageSize: string
}

const DEFAULT_K8S: K8sFormState = { name: '', image: '', cpu: '', memory: '', gpu: '', storageSize: '' }

function buildK8sYAML(f: K8sFormState): string {
  const imageLine = f.image ? `\n  image: "${f.image}"` : ''
  const storageSize = f.storageSize ? `\n  storage_size: "${f.storageSize}"` : ''
  const hasCpu = !!f.cpu, hasMem = !!f.memory, hasGpu = !!f.gpu
  const resourcesLines = (hasCpu || hasMem || hasGpu)
    ? `\n  resources:` +
      (hasCpu ? `\n    cpu: "${f.cpu}"` : '') +
      (hasMem ? `\n    memory: "${f.memory}"` : '') +
      (hasGpu ? `\n    gpu: "${f.gpu}"` : '')
    : ''
  return `metadata:
  name: ${f.name || 'my-notebook'}
spec:${imageLine}${storageSize}${resourcesLines}
`
}

// ── Bare-metal form ───────────────────────────────────────────────────────────

interface WorkerFormState {
  name: string
  env: string
  gpus: string
  worker: string
}

const DEFAULT_WORKER: WorkerFormState = { name: '', env: '', gpus: '', worker: '' }

function buildWorkerYAML(f: WorkerFormState): string {
  const envLine = f.env ? `\n  env: "${f.env}"` : ''
  const gpuLine = f.gpus ? `\n  gpus: "${f.gpus}"` : ''
  const workerLine = f.worker ? `\n  worker: ${f.worker}` : ''
  return `metadata:
  name: ${f.name || 'my-notebook'}
spec:${envLine}${gpuLine}${workerLine}
`
}

// ── Panel ──────────────────────────────────────────────────────────────────────

interface LaunchPanelProps {
  onClose: () => void
  onLaunched: () => void
  initialVolumeId?: string
  releasedVolumes: NotebookVolume[]
}

export default function LaunchPanel({ onClose, onLaunched, initialVolumeId, releasedVolumes }: LaunchPanelProps) {
  const [mode, setMode] = useState<NotebookDriverMode | null>(null)
  const [tab, setTab] = useState<'form' | 'yaml'>('form')
  const [volumeId, setVolumeId] = useState(initialVolumeId ?? '')
  const [launching, setLaunching] = useState(false)
  const [error, setError] = useState('')

  // K8s state
  const [k8sForm, setK8sForm] = useState<K8sFormState>(DEFAULT_K8S)
  const [k8sYaml, setK8sYaml] = useState(() => buildK8sYAML(DEFAULT_K8S))

  // Bare-metal state
  const [workerForm, setWorkerForm] = useState<WorkerFormState>(DEFAULT_WORKER)
  const [workerYaml, setWorkerYaml] = useState(() => buildWorkerYAML(DEFAULT_WORKER))
  const [notebookWorkers, setNotebookWorkers] = useState<NotebookWorkerInfo[]>([])

  useEffect(() => {
    getNotebookMode().then(setMode)
  }, [])

  useEffect(() => {
    if (mode === 'worker') {
      listNotebookWorkers().then(setNotebookWorkers).catch(() => {})
    }
  }, [mode])

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
      setWorkerYaml(buildWorkerYAML(next))
      return next
    })
  }

  async function handleLaunch() {
    setError('')
    const isK8s = mode === 'k8s'
    const name = isK8s ? k8sForm.name : workerForm.name
    if (tab === 'form' && !name.trim()) { setError('Server name is required.'); return }
    const payload = tab === 'form'
      ? (isK8s ? buildK8sYAML(k8sForm) : buildWorkerYAML(workerForm))
      : (isK8s ? k8sYaml : workerYaml)
    if (!payload.trim()) { setError('YAML is required.'); return }
    try {
      setLaunching(true)
      await createNotebook(payload.trim(), volumeId || undefined)
      onLaunched()
      onClose()
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setLaunching(false)
    }
  }

  const selectedVol = releasedVolumes.find(v => v.id === volumeId)

  const tabBar = (
    <div className="flex overflow-hidden rounded border border-border text-xs">
      <button type="button" onClick={() => setTab('form')}
        className={`px-3 py-1.5 transition-colors ${tab === 'form' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:text-foreground'}`}>
        Form
      </button>
      <button type="button" onClick={() => setTab('yaml')}
        className={`px-3 py-1.5 transition-colors ${tab === 'yaml' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:text-foreground'}`}>
        YAML
      </button>
    </div>
  )

  const volumeField = (
    <DataBodyTemplate.Field
      label="Volume"
      description="Attach to a released volume to recover existing data, or leave blank to provision a new one.">
      <Select value={volumeId} onValueChange={v => setVolumeId(v ?? '')}>
        <SelectTrigger size="sm">
          <SelectValue placeholder="— new volume —" />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="">new volume</SelectItem>
          {releasedVolumes.map(v => (
            <SelectItem key={v.id} value={v.id}>
              {v.label}&nbsp;·&nbsp;
              <span className="font-mono text-xs">{v.id.slice(0, 8)}</span>
              {v.work_dir ? `  ${v.work_dir}` : ''}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {selectedVol && (
        <p className="mt-1 font-mono text-xs text-muted-foreground">{selectedVol.work_dir}</p>
      )}
    </DataBodyTemplate.Field>
  )

  if (mode === null) {
    return (
      <DataPage.Group surface="bordered" className="mb-4">
        <div className="p-4 text-sm text-muted-foreground">Loading…</div>
      </DataPage.Group>
    )
  }

  return (
    <DataPage.Group surface="bordered" className="mb-4">
      <div className="p-4">
        {/* Header */}
        <div className="mb-4 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="text-sm font-semibold">Launch Notebook Server</span>
            <span className={`rounded px-1.5 py-0.5 text-xs font-medium ${mode === 'k8s' ? 'bg-blue-500/15 text-blue-400' : 'bg-orange-500/15 text-orange-400'}`}>
              {mode === 'k8s' ? 'Kubernetes' : 'Bare-metal'}
            </span>
          </div>
          <div className="flex items-center gap-2">
            {tabBar}
            <button type="button" onClick={onClose}
              className="text-xs text-muted-foreground hover:text-foreground">✕</button>
          </div>
        </div>

        {/* K8s form */}
        {mode === 'k8s' && tab === 'form' && (
          <div className="space-y-4">
            <DataBodyTemplate.Field label="Server Name">
              <Input value={k8sForm.name} onChange={e => setK8sField('name', e.target.value)}
                placeholder="my-notebook" autoFocus />
            </DataBodyTemplate.Field>

            {volumeField}

            <DataBodyTemplate.Field label="Image"
              description="Container image. Leave blank to use the cluster default.">
              <Input value={k8sForm.image} onChange={e => setK8sField('image', e.target.value)}
                placeholder="jupyter/scipy-notebook:latest" />
            </DataBodyTemplate.Field>

            <DataBodyTemplate.Field label="Storage Size"
              description="PVC size. Leave blank for the cluster default (10Gi).">
              <Input value={k8sForm.storageSize} onChange={e => setK8sField('storageSize', e.target.value)}
                placeholder="10Gi" />
            </DataBodyTemplate.Field>

            <DataBodyTemplate.Group layout="stacked" variant="bordered" title="Resources (optional)">
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
          </div>
        )}

        {/* Bare-metal form */}
        {mode === 'worker' && tab === 'form' && (
          <div className="space-y-4">
            <DataBodyTemplate.Field label="Server Name">
              <Input value={workerForm.name} onChange={e => setWorkerField('name', e.target.value)}
                placeholder="my-notebook" autoFocus />
            </DataBodyTemplate.Field>

            {volumeField}

            <DataBodyTemplate.Field label="Python Environment"
              description="venv path (e.g. /project/venv) or conda env (e.g. conda:ml-env). Leave blank to use the worker default.">
              <Input value={workerForm.env} onChange={e => setWorkerField('env', e.target.value)}
                placeholder="/home/user/project/venv" />
            </DataBodyTemplate.Field>

            <DataBodyTemplate.Field label="GPUs"
              description="Device IDs: 0 · 0,1 · all · leave blank for no GPU">
              <Input value={workerForm.gpus} onChange={e => setWorkerField('gpus', e.target.value)}
                placeholder="0" />
            </DataBodyTemplate.Field>

            <DataBodyTemplate.Field label="Worker"
              description="Launch on a specific worker node. Leave blank to auto-assign.">
              <Select value={workerForm.worker} onValueChange={v => setWorkerField('worker', v ?? '')}>
                <SelectTrigger size="sm"><SelectValue placeholder="— auto assign —" /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="">auto assign</SelectItem>
                  {notebookWorkers.map(w => (
                    <SelectItem key={w.id} value={w.hostname}>
                      {w.hostname}{w.gpus?.length ? ` (GPU: ${w.gpus.join(', ')})` : ''}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </DataBodyTemplate.Field>
          </div>
        )}

        {/* YAML editor */}
        {tab === 'yaml' && (
          <textarea
            className="w-full resize-none rounded border border-border bg-background p-3 font-mono text-sm focus:outline-none"
            rows={12}
            value={mode === 'k8s' ? k8sYaml : workerYaml}
            onChange={e => mode === 'k8s' ? setK8sYaml(e.target.value) : setWorkerYaml(e.target.value)}
            spellCheck={false}
          />
        )}

        {error && <p className="mt-3 text-sm text-destructive">{error}</p>}

        <div className="mt-4 flex justify-end gap-2">
          <Button variant="outline" size="sm" onClick={onClose}>Cancel</Button>
          <Button size="sm" onClick={() => void handleLaunch()} disabled={launching}>
            {launching ? 'Launching…' : volumeId ? 'Attach & Launch' : 'Launch'}
          </Button>
        </div>
      </div>
    </DataPage.Group>
  )
}
