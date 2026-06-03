import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import {
  DataBodyTemplate,
  Select, SelectTrigger, SelectContent, SelectItem, SelectValue,
} from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { YamlMirror } from '@/components/ui/yaml-mirror'
import {
  createNotebook, listNotebookVolumes, listNotebookWorkers,
  type NotebookVolume, type NotebookWorkerInfo,
} from '@/features/notebooks/api'

// ── YAML builders ─────────────────────────────────────────────────────────────

interface K8sFormState {
  name: string
  image: string
  cpu: string
  memory: string
  gpu: string
  storageSize: string
  prepareBackend: 'k8s'
  prepare: string
}

const DEFAULT_K8S: K8sFormState = {
  name: '',
  image: '',
  cpu: '',
  memory: '',
  gpu: '',
  storageSize: '',
  prepareBackend: 'k8s',
  prepare: '',
}

function buildK8sYAML(f: K8sFormState, workerID?: string): string {
  const lines: string[] = [`metadata:`, `  name: ${f.name || 'my-notebook'}`, `spec:`, `  k8s:`]
  if (f.image)       lines.push(`    image: "${f.image}"`)
  if (f.storageSize) lines.push(`    storage_size: "${f.storageSize}"`)
  appendPrepareSteps(lines, f.prepare, f.prepareBackend)

  const hasCpu = !!f.cpu, hasMem = !!f.memory, hasGpu = !!f.gpu
  if (hasCpu || hasMem || hasGpu) {
    lines.push(`    pod_template:`, `      spec:`, `        containers:`, `          - name: notebook`, `            resources:`)
    const requests: string[] = []
    const limits: string[] = []
    if (hasCpu) requests.push(`                cpu: "${f.cpu}"`)
    if (hasMem) { requests.push(`                memory: "${f.memory}"`); limits.push(`                memory: "${f.memory}"`) }
    if (hasGpu) limits.push(`                nvidia.com/gpu: "${f.gpu}"`)
    if (requests.length) { lines.push(`              requests:`); lines.push(...requests) }
    if (limits.length)   { lines.push(`              limits:`);   lines.push(...limits) }
  }
  if (workerID) lines.push(`  placement:`, `    worker: ${workerID}`)
  return lines.join('\n') + '\n'
}

interface WorkerFormState {
  name: string
  env: string
  gpus: string
  prepareBackend: 'process' | 'docker'
  prepare: string
}

const DEFAULT_WORKER: WorkerFormState = {
  name: '',
  env: '',
  gpus: '',
  prepareBackend: 'process',
  prepare: '',
}

function buildWorkerYAML(f: WorkerFormState, workerID?: string): string {
  return buildWorkerYAMLWithBackend(f, workerID, f.prepareBackend)
}

function buildWorkerYAMLWithBackend(
  f: WorkerFormState,
  workerID: string | undefined,
  backend: 'process' | 'docker' | 'k8s',
): string {
  const lines: string[] = [`metadata:`, `  name: ${f.name || 'my-notebook'}`, `spec:`]
  if (f.env || f.gpus) {
    lines.push(`  process:`)
    if (f.env)  lines.push(`    env: "${f.env}"`)
    if (f.gpus) lines.push(`    gpus: "${f.gpus}"`)
  }
  appendPrepareSteps(lines, f.prepare, backend)
  if (workerID) lines.push(`  placement:`, `    worker: ${workerID}`)
  return lines.join('\n') + '\n'
}

function appendPrepareSteps(lines: string[], text: string, backend: 'process' | 'docker' | 'k8s') {
  const commands = text
    .split('\n')
    .map(line => line.trim())
    .filter(Boolean)
  if (commands.length === 0) return
  lines.push(`  prepare:`, `    steps:`)
  for (const cmd of commands) {
    lines.push(
      `      - type: command`,
      `        backend: ${backend}`,
      `        command: ["sh", "-lc", "${escapeYamlDoubleQuoted(cmd)}"]`,
    )
  }
}

function escapeYamlDoubleQuoted(value: string): string {
  return value
    .replace(/\\/g, '\\\\')
    .replace(/"/g, '\\"')
}

function normalizePrepareBackend(mode?: string): 'process' | 'docker' {
  return mode === 'docker' ? 'docker' : 'process'
}

// ── Page ──────────────────────────────────────────────────────────────────────

export default function NotebookCreatePage() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const preselectedVolume = searchParams.get('volume') ?? ''

  const [workers, setWorkers] = useState<NotebookWorkerInfo[]>([])
  const [selectedWorkerID, setSelectedWorkerID] = useState('')
  const [tab, setTab] = useState('form')

  const [k8sForm, setK8sForm] = useState<K8sFormState>(DEFAULT_K8S)
  const [k8sYaml, setK8sYaml] = useState(() => buildK8sYAML(DEFAULT_K8S))

  const [workerForm, setWorkerForm] = useState<WorkerFormState>(DEFAULT_WORKER)
  const [workerYaml, setWorkerYaml] = useState(() => buildWorkerYAML(DEFAULT_WORKER))

  const [volumeId, setVolumeId] = useState(preselectedVolume)
  const [releasedVolumes, setReleasedVolumes] = useState<NotebookVolume[]>([])

  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    listNotebookWorkers().then(setWorkers).catch(() => {})
    listNotebookVolumes().then(vols => setReleasedVolumes(vols.filter(v => v.status === 'released'))).catch(() => {})
  }, [])

  // Derive selected worker object and its runtime
  const selectedWorker = useMemo(
    () => workers.find(w => w.id === selectedWorkerID) ?? null,
    [workers, selectedWorkerID],
  )

  // runtime is driven by the selected worker; falls back to first available kind
  const runtime = useMemo<'k8s' | 'baremetal'>(() => {
    if (selectedWorker) return selectedWorker.kind
    if (workers.some(w => w.kind === 'baremetal')) return 'baremetal'
    if (workers.some(w => w.kind === 'k8s')) return 'k8s'
    return 'baremetal'
  }, [selectedWorker, workers])

  const workerLabel = (w: NotebookWorkerInfo) => {
    const base = w.kind === 'k8s' ? (w.cluster_name || w.hostname || w.id) : (w.hostname || w.id)
    const mode = w.kind === 'baremetal' && w.mode ? ` · ${w.mode}` : ''
    const gpu = w.gpus?.length ? `  (GPU: ${w.gpus.join(', ')})` : ''
    return `${base}${mode}${gpu}`
  }

  const workerPrepareBackend = useMemo<'process' | 'docker' | 'k8s'>(() => {
    if (selectedWorker?.kind === 'k8s') return 'k8s'
    if (selectedWorker?.kind === 'baremetal') return normalizePrepareBackend(selectedWorker.mode)
    return workerForm.prepareBackend
  }, [selectedWorker, workerForm.prepareBackend])

  function setK8sField<K extends keyof K8sFormState>(key: K, value: K8sFormState[K]) {
    setK8sForm(prev => {
      const next = { ...prev, [key]: value }
      setK8sYaml(buildK8sYAML(next, selectedWorker?.hostname))
      return next
    })
  }

  function setWorkerField<K extends keyof WorkerFormState>(key: K, value: WorkerFormState[K]) {
    setWorkerForm(prev => {
      const next = { ...prev, [key]: value }
      const backend =
        selectedWorker?.kind === 'baremetal'
          ? normalizePrepareBackend(selectedWorker.mode)
          : next.prepareBackend
      setWorkerYaml(buildWorkerYAMLWithBackend(next, selectedWorker?.hostname, backend))
      return next
    })
  }

  function onWorkerChange(id: string | null) {
    setSelectedWorkerID(id ?? '')
    const w = workers.find(x => x.id === id) ?? null
    setK8sYaml(buildK8sYAML(k8sForm, w?.hostname))
    if (w?.kind === 'baremetal' && w.mode) {
      const backend = normalizePrepareBackend(w.mode)
      setWorkerForm(prev => ({ ...prev, prepareBackend: backend }))
      setWorkerYaml(buildWorkerYAMLWithBackend(workerForm, w?.hostname, backend))
    } else {
      setWorkerYaml(buildWorkerYAML(workerForm, w?.hostname))
    }
  }

  async function handleSubmit() {
    setError('')
    const isK8s = runtime === 'k8s'
    const name = isK8s ? k8sForm.name : workerForm.name
    if (tab === 'form' && !name.trim()) { setError('Server name is required.'); return }
    const payload = tab === 'form'
      ? (isK8s
        ? buildK8sYAML(k8sForm, selectedWorker?.hostname)
        : buildWorkerYAMLWithBackend(workerForm, selectedWorker?.hostname, workerPrepareBackend))
      : (isK8s ? k8sYaml : workerYaml)
    if (!payload.trim()) { setError('YAML is required.'); return }
    try {
      setSubmitting(true)
      await createNotebook(payload.trim(), volumeId || undefined)
      navigate('/notebooks')
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSubmitting(false)
    }
  }

  const selectedVol = releasedVolumes.find(v => v.id === volumeId)

  const runtimeBadge = (
    <span className={`rounded px-2 py-0.5 text-xs font-medium ${
      runtime === 'k8s' ? 'bg-blue-500/15 text-blue-400' : 'bg-orange-500/15 text-orange-400'
    }`}>
      {runtime === 'k8s' ? 'Kubernetes' : 'Bare-metal'}
    </span>
  )

  const volumeField = (
    <DataBodyTemplate.Field label="Volume" description="Attach to a released volume to recover existing data, or leave blank to provision a new one.">
      <Select value={volumeId} onValueChange={v => setVolumeId(v ?? '')}>
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

  const workerField = (
    <DataBodyTemplate.Field
      label="Worker"
      description="Select a specific worker. Leave blank to auto-assign."
    >
      <Select value={selectedWorkerID} onValueChange={onWorkerChange}>
        <SelectTrigger size="sm"><SelectValue placeholder="— auto assign —" /></SelectTrigger>
        <SelectContent>
          <SelectItem value="">auto assign</SelectItem>
          {workers.map(w => (
            <SelectItem key={w.id} value={w.id}>
              <span className={`mr-1.5 rounded px-1 py-0.5 text-[10px] font-medium ${
                w.kind === 'k8s' ? 'bg-blue-500/15 text-blue-400' : 'bg-orange-500/15 text-orange-400'
              }`}>
                {w.kind === 'k8s' ? 'K8s' : 'BM'}
              </span>
              {workerLabel(w)}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </DataBodyTemplate.Field>
  )

  return (
    <DataBodyTemplate
      title="Launch Notebook Server"
      description={runtimeBadge}
      activeTab={tab}
      onTabChange={setTab}
      actions={
        <div className="flex items-center gap-2">
          {error && <span className="text-sm text-destructive">{error}</span>}
          <Button variant="outline" size="sm" onClick={() => navigate('/notebooks')}>Cancel</Button>
          <Button size="sm" onClick={() => void handleSubmit()} disabled={submitting}>
            {submitting ? 'Launching…' : volumeId ? 'Attach & Launch' : 'Launch'}
          </Button>
        </div>
      }
    >
      {/* ── Form tab ── */}
      <DataBodyTemplate.Tab id="form" label="Form">
        {/* Worker selection always visible — drives runtime detection */}
        <DataBodyTemplate.Group layout="stacked">
          {workerField}
        </DataBodyTemplate.Group>

        {runtime === 'k8s' && (
          <>
            <DataBodyTemplate.Group layout="stacked">
              <DataBodyTemplate.Field label="Server Name">
                <Input value={k8sForm.name} onChange={e => setK8sField('name', e.target.value)} placeholder="my-notebook" autoFocus />
              </DataBodyTemplate.Field>
              {volumeField}
              <DataBodyTemplate.Field label="Image" description="Container image. Leave blank to use the cluster default.">
                <Input value={k8sForm.image} onChange={e => setK8sField('image', e.target.value)} placeholder="jupyter/scipy-notebook:latest" />
              </DataBodyTemplate.Field>
              <DataBodyTemplate.Field label="Storage Size" description="PVC size. Leave blank for the cluster default (10Gi).">
                <Input value={k8sForm.storageSize} onChange={e => setK8sField('storageSize', e.target.value)} placeholder="10Gi" />
              </DataBodyTemplate.Field>
              <DataBodyTemplate.Field label="Prepare Commands" description="One command per line. Runs before notebook start.">
                <textarea
                  className="min-h-28 w-full resize-y rounded-lg border border-border bg-card px-3 py-2 font-mono text-sm focus:border-primary focus:outline-none"
                  value={k8sForm.prepare}
                  onChange={e => setK8sField('prepare', e.target.value)}
                  placeholder={`pip install -r requirements.txt\npython /work/preflight.py`}
                />
              </DataBodyTemplate.Field>
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

        {runtime === 'baremetal' && (
          <DataBodyTemplate.Group layout="stacked">
            <DataBodyTemplate.Field label="Server Name">
              <Input value={workerForm.name} onChange={e => setWorkerField('name', e.target.value)} placeholder="my-notebook" autoFocus />
            </DataBodyTemplate.Field>
            {volumeField}
            <DataBodyTemplate.Field label="Python Environment" description="venv path (e.g. /project/venv) or conda env (e.g. conda:ml-env). Leave blank to use the worker default.">
              <Input value={workerForm.env} onChange={e => setWorkerField('env', e.target.value)} placeholder="/home/user/project/venv" />
            </DataBodyTemplate.Field>
            <DataBodyTemplate.Field label="GPUs" description="Device IDs: 0 · 0,1 · all · leave blank for no GPU">
              <Input value={workerForm.gpus} onChange={e => setWorkerField('gpus', e.target.value)} placeholder="0" />
            </DataBodyTemplate.Field>
            <DataBodyTemplate.Field label="Prepare Backend" description="Follows the selected bare-metal worker mode; manual selection is used only when auto-assigning.">
              <Select
                value={workerPrepareBackend}
                disabled={selectedWorker?.kind === 'baremetal'}
                onValueChange={v => setWorkerField('prepareBackend', (v ?? 'process') as WorkerFormState['prepareBackend'])}
              >
                <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="process">process</SelectItem>
                  <SelectItem value="docker">docker</SelectItem>
                </SelectContent>
              </Select>
            </DataBodyTemplate.Field>
            <DataBodyTemplate.Field label="Prepare Commands" description="One command per line. Runs before notebook start.">
              <textarea
                className="min-h-28 w-full resize-y rounded-lg border border-border bg-card px-3 py-2 font-mono text-sm focus:border-primary focus:outline-none"
                value={workerForm.prepare}
                onChange={e => setWorkerField('prepare', e.target.value)}
                placeholder={`uv pip install jupyterlab ipykernel\npython -m ipykernel install --sys-prefix`}
              />
            </DataBodyTemplate.Field>
          </DataBodyTemplate.Group>
        )}
      </DataBodyTemplate.Tab>

      {/* ── YAML tab ── */}
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
