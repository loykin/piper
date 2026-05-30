import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { DataGrid, DataGridPaginationCompact, type DataGridColumnDef } from '@loykin/gridkit'
import { DataPage } from '@loykin/designkit'
import {
  listServing, deployServing, stopServing, restartServing,
  listRuns, listArtifacts,
  type Service, type Run, type StepArtifacts,
} from '../api'
import StatusBadge from '../components/StatusBadge'

// ─── Runtime templates ────────────────────────────────────────────────────────

interface RuntimeTemplate {
  label: string
  description: string
  runtimeMode: string
  image: string
  imageOptions?: string[]
  command: string
  port: string
  healthPath: string
}

const RUNTIME_TEMPLATES: Record<string, RuntimeTemplate> = {
  custom: {
    label: 'Custom',
    description: 'Custom Python script',
    runtimeMode: 'local',
    image: '',
    command: 'python\nserve.py',
    port: '8000',
    healthPath: '/',
  },
  triton: {
    label: 'Triton',
    description: 'NVIDIA Triton Inference Server (local)',
    runtimeMode: 'local',
    image: '',
    command: 'tritonserver\n--model-repository=$PIPER_MODEL_DIR\n--http-port=8000',
    port: '8000',
    healthPath: '/v2/health/ready',
  },
  torchserve: {
    label: 'TorchServe',
    description: 'PyTorch model server (local)',
    runtimeMode: 'local',
    image: '',
    command: 'torchserve\n--start\n--model-store=$PIPER_MODEL_DIR\n--foreground',
    port: '8080',
    healthPath: '/ping',
  },
  mlflow: {
    label: 'MLflow',
    description: 'MLflow built-in model server (local)',
    runtimeMode: 'local',
    image: '',
    command: 'mlflow\nmodels\nserve\n-m $PIPER_MODEL_DIR\n--port=5000\n--no-conda',
    port: '5000',
    healthPath: '/health',
  },
  vllm: {
    label: 'vLLM',
    description: 'High-throughput LLM inference engine (local)',
    runtimeMode: 'local',
    image: '',
    command: 'python\n-m vllm.entrypoints.openai.api_server\n--model=$PIPER_MODEL_DIR\n--port=8000',
    port: '8000',
    healthPath: '/health',
  },
  ollama: {
    label: 'Ollama',
    description: 'Local LLM runtime',
    runtimeMode: 'local',
    image: '',
    command: 'ollama\nserve',
    port: '11434',
    healthPath: '/api/version',
  },
  triton_k8s: {
    label: 'Triton (k8s)',
    description: 'NVIDIA Triton on Kubernetes',
    runtimeMode: 'k8s',
    image: 'nvcr.io/nvidia/tritonserver:24.12-py3',
    imageOptions: [
      'nvcr.io/nvidia/tritonserver:24.12-py3',
      'nvcr.io/nvidia/tritonserver:24.09-py3',
    ],
    command: 'tritonserver\n--model-repository=$PIPER_MODEL_DIR\n--http-port=8000',
    port: '8000',
    healthPath: '/v2/health/ready',
  },
  vllm_k8s: {
    label: 'vLLM (k8s)',
    description: 'vLLM inference engine on Kubernetes',
    runtimeMode: 'k8s',
    image: 'vllm/vllm-openai:latest',
    imageOptions: ['vllm/vllm-openai:latest', 'vllm/vllm-openai:v0.6.6'],
    command: 'python\n-m vllm.entrypoints.openai.api_server\n--model=$PIPER_MODEL_DIR\n--port=8000',
    port: '8000',
    healthPath: '/health',
  },
  torchserve_k8s: {
    label: 'TorchServe (k8s)',
    description: 'PyTorch TorchServe on Kubernetes',
    runtimeMode: 'k8s',
    image: 'pytorch/torchserve:latest-gpu',
    imageOptions: ['pytorch/torchserve:latest-gpu', 'pytorch/torchserve:latest'],
    command: 'torchserve\n--start\n--model-store=$PIPER_MODEL_DIR\n--foreground',
    port: '8080',
    healthPath: '/ping',
  },
  mlflow_k8s: {
    label: 'MLflow (k8s)',
    description: 'MLflow model server on Kubernetes',
    runtimeMode: 'k8s',
    image: 'ghcr.io/mlflow/mlflow:v2.19.0',
    imageOptions: ['ghcr.io/mlflow/mlflow:v2.19.0', 'ghcr.io/mlflow/mlflow:latest'],
    command: 'mlflow\nmodels\nserve\n-m $PIPER_MODEL_DIR\n--port=5000\n--no-conda\n--host=0.0.0.0',
    port: '5000',
    healthPath: '/health',
  },
}

// ─── YAML builder ─────────────────────────────────────────────────────────────

interface FormState {
  name: string
  pipeline: string
  run: string
  step: string
  artifact: string
  templateKey: string
  runtimeMode: string
  image: string
  command: string
  port: string
  healthPath: string
  k8sNamespace: string
  k8sReplicas: string
  k8sCPU: string
  k8sMemory: string
  k8sGPU: string
  k8sImagePullPolicy: string
}

const DEFAULT_FORM: FormState = {
  name: '',
  pipeline: '',
  run: 'latest',
  step: '',
  artifact: '',
  templateKey: 'custom',
  runtimeMode: 'local',
  image: '',
  command: 'python\nserve.py',
  port: '8000',
  healthPath: '/',
  k8sNamespace: 'default',
  k8sReplicas: '1',
  k8sCPU: '',
  k8sMemory: '',
  k8sGPU: '',
  k8sImagePullPolicy: 'Always',
}

function buildYAML(f: FormState): string {
  const cmdLines = f.command.split('\n').filter(Boolean).map(c => `      - ${c}`).join('\n')
  const isK8s = f.runtimeMode === 'k8s'

  let k8sSection = ''
  if (isK8s) {
    const resources: string[] = []
    if (f.k8sCPU)    resources.push(`      cpu: "${f.k8sCPU}"`)
    if (f.k8sMemory) resources.push(`      memory: "${f.k8sMemory}"`)
    if (f.k8sGPU)    resources.push(`      gpu: "${f.k8sGPU}"`)
    k8sSection = `  k8s:
    namespace: ${f.k8sNamespace || 'default'}
    replicas: ${f.k8sReplicas || '1'}
    image_pull_policy: ${f.k8sImagePullPolicy || 'Always'}${
      resources.length ? `\n    resources:\n${resources.join('\n')}` : ''
    }
`
  }

  const imageField = isK8s && f.image ? `    image: ${f.image}\n` : ''

  return `apiVersion: piper/v1
kind: ModelService
metadata:
  name: ${f.name || 'my-model'}
spec:
  model:
    from_artifact:
      pipeline: ${f.pipeline || 'my-pipeline'}
      step: ${f.step || 'train'}
      artifact: ${f.artifact || 'model'}
      run: ${f.run || 'latest'}
  runtime:
    mode: ${f.runtimeMode}
${imageField}    command:
${cmdLines || '      - python\n      - serve.py'}
    port: ${f.port || '8000'}
    health_path: ${f.healthPath || '/'}
${k8sSection}`
}

// ─── Deploy panel ─────────────────────────────────────────────────────────────

function DeployPanel({ onClose, onDeployed }: { onClose: () => void; onDeployed: () => void }) {
  const [form, setForm] = useState<FormState>(DEFAULT_FORM)
  const [tab, setTab] = useState<'form' | 'yaml'>('form')
  const [yaml, setYaml] = useState(() => buildYAML(DEFAULT_FORM))
  const [deploying, setDeploying] = useState(false)
  const [error, setError] = useState('')
  const [successRuns, setSuccessRuns] = useState<Run[]>([])
  const [artifacts, setArtifacts] = useState<StepArtifacts[]>([])

  useEffect(() => {
    listRuns({ status: 'success' }).then(setSuccessRuns).catch(() => {})
  }, [])

  const pipelines = Array.from(new Set(successRuns.map(r => r.pipeline_name))).sort()
  const pipelineRuns = successRuns.filter(r => r.pipeline_name === form.pipeline)

  useEffect(() => {
    if (!form.pipeline) { setArtifacts([]); return }
    const runId = form.run === 'latest' ? pipelineRuns[0]?.id : form.run
    if (!runId) { setArtifacts([]); return }
    listArtifacts(runId).then(setArtifacts).catch(() => setArtifacts([]))
  }, [form.pipeline, form.run, pipelineRuns[0]?.id])

  const steps = artifacts.map(sa => sa.step)
  const artifactNames = artifacts.find(sa => sa.step === form.step)?.artifacts.map(a => a.name) ?? []

  function setField<K extends keyof FormState>(key: K, value: FormState[K]) {
    setForm(prev => {
      const next = { ...prev, [key]: value }
      if (key === 'pipeline') { next.run = 'latest'; next.step = ''; next.artifact = '' }
      if (key === 'run') { next.step = ''; next.artifact = '' }
      if (key === 'step') { next.artifact = '' }
      if (key === 'command' || key === 'port' || key === 'healthPath') next.templateKey = 'custom'
      setYaml(buildYAML(next))
      return next
    })
  }

  async function handleDeploy() {
    setError('')
    const payload = tab === 'form' ? buildYAML(form) : yaml
    if (!payload.trim()) { setError('YAML is required.'); return }
    try {
      setDeploying(true)
      await deployServing(payload.trim())
      onDeployed()
      onClose()
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setDeploying(false)
    }
  }

  return (
    <DataPage.Group surface="bordered" className="mb-4">
      <div className="p-4">
        <div className="mb-4 flex items-center justify-between">
          <span className="text-sm font-semibold">Deploy ModelService</span>
          <div className="flex items-center gap-2">
            <div className="flex overflow-hidden rounded border border-border text-xs">
              <button type="button" onClick={() => { if (tab !== 'form') setTab('form') }}
                className={`px-3 py-1.5 transition-colors ${tab === 'form' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:text-foreground'}`}>
                Form
              </button>
              <button type="button" onClick={() => { setYaml(buildYAML(form)); setTab('yaml') }}
                className={`px-3 py-1.5 transition-colors ${tab === 'yaml' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:text-foreground'}`}>
                YAML
              </button>
            </div>
            <button type="button" onClick={onClose}
              className="text-xs text-muted-foreground hover:text-foreground">
              ✕ Cancel
            </button>
          </div>
        </div>

        {tab === 'form' ? (
          <div className="space-y-4">
            <div>
              <label className="mb-1 block text-xs font-medium text-muted-foreground">Service Name</label>
              <input
                className="w-full rounded border border-border bg-background px-3 py-2 text-sm focus:border-ring focus:outline-none"
                value={form.name}
                onChange={e => setField('name', e.target.value)}
                placeholder="my-model"
              />
            </div>

            <fieldset className="space-y-3 rounded border border-border p-4">
              <legend className="px-1 text-xs font-semibold text-muted-foreground">Model Source</legend>
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="mb-1 block text-xs text-muted-foreground">Pipeline</label>
                  <select
                    className="w-full rounded border border-border bg-background px-3 py-2 text-sm focus:outline-none"
                    value={form.pipeline}
                    onChange={e => setField('pipeline', e.target.value)}
                  >
                    <option value="">— select pipeline —</option>
                    {pipelines.map(p => <option key={p} value={p}>{p}</option>)}
                  </select>
                </div>
                <div>
                  <label className="mb-1 block text-xs text-muted-foreground">Run</label>
                  <select
                    className="w-full rounded border border-border bg-background px-3 py-2 text-sm focus:outline-none disabled:opacity-50"
                    value={form.run}
                    onChange={e => setField('run', e.target.value)}
                    disabled={!form.pipeline}
                  >
                    <option value="latest">latest</option>
                    {pipelineRuns.map(r => (
                      <option key={r.id} value={r.id}>{r.id.slice(0, 24)}… {r.started_at ? new Date(r.started_at).toLocaleDateString() : ''}</option>
                    ))}
                  </select>
                </div>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="mb-1 block text-xs text-muted-foreground">Step</label>
                  <select
                    className="w-full rounded border border-border bg-background px-3 py-2 text-sm focus:outline-none disabled:opacity-50"
                    value={form.step}
                    onChange={e => setField('step', e.target.value)}
                    disabled={steps.length === 0}
                  >
                    <option value="">— select step —</option>
                    {steps.map(s => <option key={s} value={s}>{s}</option>)}
                  </select>
                </div>
                <div>
                  <label className="mb-1 block text-xs text-muted-foreground">Artifact</label>
                  <select
                    className="w-full rounded border border-border bg-background px-3 py-2 text-sm focus:outline-none disabled:opacity-50"
                    value={form.artifact}
                    onChange={e => setField('artifact', e.target.value)}
                    disabled={artifactNames.length === 0}
                  >
                    <option value="">— select artifact —</option>
                    {artifactNames.map(a => <option key={a} value={a}>{a}</option>)}
                  </select>
                </div>
              </div>
            </fieldset>

            <fieldset className="space-y-3 rounded border border-border p-4">
              <legend className="px-1 text-xs font-semibold text-muted-foreground">Runtime</legend>

              <div className="flex flex-wrap gap-1.5">
                {Object.entries(RUNTIME_TEMPLATES).map(([key, tpl]) => (
                  <button
                    key={key}
                    type="button"
                    title={tpl.description}
                    onClick={() => setForm(prev => ({
                      ...prev,
                      templateKey: key,
                      runtimeMode: tpl.runtimeMode,
                      image: tpl.image,
                      command: tpl.command,
                      port: tpl.port,
                      healthPath: tpl.healthPath,
                    }))}
                    className={`rounded-full border px-2.5 py-1 text-xs font-medium transition-colors ${
                      form.templateKey === key
                        ? 'border-primary bg-primary/10 text-primary'
                        : 'border-border text-muted-foreground hover:border-foreground/30 hover:text-foreground'
                    }`}
                  >
                    {tpl.label}
                  </button>
                ))}
              </div>

              <div className="grid grid-cols-3 gap-3">
                <div>
                  <label className="mb-1 block text-xs text-muted-foreground">Mode</label>
                  <select
                    className="w-full rounded border border-border bg-background px-3 py-2 text-sm focus:outline-none"
                    value={form.runtimeMode}
                    onChange={e => setField('runtimeMode', e.target.value)}
                  >
                    <option value="local">local</option>
                    <option value="k8s">k8s</option>
                  </select>
                </div>
                <div>
                  <label className="mb-1 block text-xs text-muted-foreground">Port</label>
                  <input
                    className="w-full rounded border border-border bg-background px-3 py-2 text-sm focus:outline-none"
                    value={form.port}
                    onChange={e => setField('port', e.target.value)}
                    placeholder="8000"
                  />
                </div>
                <div>
                  <label className="mb-1 block text-xs text-muted-foreground">Health Path</label>
                  <input
                    className="w-full rounded border border-border bg-background px-3 py-2 text-sm focus:outline-none"
                    value={form.healthPath}
                    onChange={e => setField('healthPath', e.target.value)}
                    placeholder="/"
                  />
                </div>
              </div>

              {form.runtimeMode === 'k8s' && (
                <>
                  <div>
                    <label className="mb-1 block text-xs text-muted-foreground">Container Image</label>
                    <input
                      className="w-full rounded border border-border bg-background px-3 py-2 text-sm focus:outline-none"
                      value={form.image}
                      onChange={e => setField('image', e.target.value)}
                      placeholder="registry/image:tag"
                    />
                  </div>
                  <div className="grid grid-cols-3 gap-3">
                    <div>
                      <label className="mb-1 block text-xs text-muted-foreground">Namespace</label>
                      <input className="w-full rounded border border-border bg-background px-3 py-2 text-sm focus:outline-none" value={form.k8sNamespace} onChange={e => setField('k8sNamespace', e.target.value)} placeholder="default" />
                    </div>
                    <div>
                      <label className="mb-1 block text-xs text-muted-foreground">Replicas</label>
                      <input className="w-full rounded border border-border bg-background px-3 py-2 text-sm focus:outline-none" type="number" min="1" value={form.k8sReplicas} onChange={e => setField('k8sReplicas', e.target.value)} />
                    </div>
                    <div>
                      <label className="mb-1 block text-xs text-muted-foreground">Image Pull Policy</label>
                      <select className="w-full rounded border border-border bg-background px-3 py-2 text-sm focus:outline-none" value={form.k8sImagePullPolicy} onChange={e => setField('k8sImagePullPolicy', e.target.value)}>
                        <option value="Always">Always</option>
                        <option value="IfNotPresent">IfNotPresent</option>
                        <option value="Never">Never</option>
                      </select>
                    </div>
                  </div>
                  <div className="grid grid-cols-3 gap-3">
                    <div>
                      <label className="mb-1 block text-xs text-muted-foreground">CPU</label>
                      <input className="w-full rounded border border-border bg-background px-3 py-2 text-sm focus:outline-none" value={form.k8sCPU} onChange={e => setField('k8sCPU', e.target.value)} placeholder="2" />
                    </div>
                    <div>
                      <label className="mb-1 block text-xs text-muted-foreground">Memory</label>
                      <input className="w-full rounded border border-border bg-background px-3 py-2 text-sm focus:outline-none" value={form.k8sMemory} onChange={e => setField('k8sMemory', e.target.value)} placeholder="4Gi" />
                    </div>
                    <div>
                      <label className="mb-1 block text-xs text-muted-foreground">GPU</label>
                      <input className="w-full rounded border border-border bg-background px-3 py-2 text-sm focus:outline-none" value={form.k8sGPU} onChange={e => setField('k8sGPU', e.target.value)} placeholder="1" />
                    </div>
                  </div>
                </>
              )}

              <div>
                <label className="mb-1 block text-xs text-muted-foreground">
                  Command <span className="text-muted-foreground/50">(one argument per line)</span>
                </label>
                <textarea
                  className="w-full resize-none rounded border border-border bg-background px-3 py-2 font-mono text-sm focus:outline-none"
                  rows={4}
                  value={form.command}
                  onChange={e => setField('command', e.target.value)}
                  spellCheck={false}
                />
                <p className="mt-0.5 text-xs text-muted-foreground/60">
                  <code className="text-primary">$PIPER_MODEL_DIR</code> points to the artifact directory
                </p>
              </div>
            </fieldset>
          </div>
        ) : (
          <textarea
            className="w-full resize-none rounded border border-border bg-background p-3 font-mono text-sm focus:outline-none"
            rows={20}
            value={yaml}
            onChange={e => setYaml(e.target.value)}
            spellCheck={false}
          />
        )}

        {error && <p className="mt-3 text-sm text-destructive">{error}</p>}

        <div className="mt-4 flex justify-end gap-2">
          <button type="button" onClick={onClose}
            className="rounded border border-border px-4 py-2 text-sm font-medium text-muted-foreground hover:text-foreground">
            Cancel
          </button>
          <button type="button" onClick={handleDeploy} disabled={deploying}
            className="rounded bg-primary px-5 py-2 text-sm font-semibold text-primary-foreground hover:opacity-90 disabled:opacity-50">
            {deploying ? 'Deploying…' : 'Deploy'}
          </button>
        </div>
      </div>
    </DataPage.Group>
  )
}

// ─── Main page ────────────────────────────────────────────────────────────────

export default function ServingPage() {
  const [services, setServices] = useState<Service[]>([])
  const [loading, setLoading] = useState(true)
  const [showDeploy, setShowDeploy] = useState(false)
  const intervalRef = useRef<ReturnType<typeof setInterval>>(0 as unknown as ReturnType<typeof setInterval>)

  const load = () =>
    listServing()
      .then(setServices)
      .catch(() => {})
      .finally(() => setLoading(false))

  useEffect(() => {
    load()
    intervalRef.current = setInterval(load, 5000)
    return () => clearInterval(intervalRef.current)
  }, [])

  async function handleStop(name: string) {
    if (!confirm(`Stop service "${name}"?`)) return
    try { await stopServing(name); await load() } catch { /* no-op */ }
  }

  async function handleRestart(name: string) {
    try { await restartServing(name); await load() } catch { /* no-op */ }
  }

  const columns: DataGridColumnDef<Service>[] = [
    {
      accessorKey: 'name',
      header: 'Name',
      meta: { minWidth: 180 },
      cell: ({ row }) => (
        <Link to={`/serving/${row.original.name}`} className="font-medium text-primary hover:underline">
          {row.original.name}
        </Link>
      ),
    },
    {
      accessorKey: 'status',
      header: 'Status',
      meta: { minWidth: 110 },
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      accessorKey: 'artifact',
      header: 'Artifact',
      meta: { minWidth: 180, flex: 1 },
      cell: ({ row }) => <span className="font-mono text-xs text-muted-foreground">{row.original.artifact || '—'}</span>,
    },
    {
      id: 'namespace',
      header: 'Namespace',
      meta: { minWidth: 110 },
      cell: ({ row }) => (
        <span className="text-xs text-muted-foreground">{row.original.namespace || 'local'}</span>
      ),
    },
    {
      accessorKey: 'endpoint',
      header: 'Endpoint',
      meta: { minWidth: 200 },
      cell: ({ row }) => {
        const ep = row.original.endpoint
        return ep ? (
          <a href={ep} target="_blank" rel="noreferrer" className="font-mono text-xs text-primary hover:underline">{ep}</a>
        ) : <span className="text-xs text-muted-foreground">—</span>
      },
    },
    {
      accessorKey: 'updated_at',
      header: 'Updated',
      meta: { minWidth: 160 },
      cell: ({ row }) => <span className="text-xs text-muted-foreground">{new Date(row.original.updated_at).toLocaleString()}</span>,
    },
    {
      id: 'actions',
      header: '',
      meta: { minWidth: 140 },
      cell: ({ row }) => {
        const svc = row.original
        return (
          <div className="flex items-center gap-2">
            {svc.status === 'running' && (
              <button type="button" onClick={e => { e.stopPropagation(); handleRestart(svc.name) }}
                className="rounded border border-border px-2 py-0.5 text-xs text-muted-foreground hover:text-foreground">
                Restart
              </button>
            )}
            {svc.status !== 'stopped' && (
              <button type="button" onClick={e => { e.stopPropagation(); handleStop(svc.name) }}
                className="rounded border border-destructive/40 px-2 py-0.5 text-xs text-destructive hover:bg-destructive/10">
                Stop
              </button>
            )}
          </div>
        )
      },
    },
  ]

  return (
    <DataPage>
      <DataPage.Header>
        <DataPage.TitleBlock
          title="Serving"
          description="Model serving endpoints deployed from pipeline artifacts."
        />
        {!showDeploy && (
          <DataPage.Actions>
            <button type="button" onClick={() => setShowDeploy(true)}
              className="rounded-md bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground hover:opacity-90">
              Deploy
            </button>
          </DataPage.Actions>
        )}
      </DataPage.Header>

      <DataPage.Content>
        {showDeploy && (
          <DeployPanel onClose={() => setShowDeploy(false)} onDeployed={load} />
        )}

        {loading ? (
          <div className="py-8 text-sm text-muted-foreground">Loading…</div>
        ) : services.length === 0 ? (
          <div className="py-12 text-center">
            <p className="text-sm text-muted-foreground">No services deployed yet.</p>
            <p className="mt-1 text-xs text-muted-foreground/60">
              Deploy a ModelService from a pipeline artifact.
            </p>
          </div>
        ) : (
          <DataPage.Group surface="none" className="h-full">
            <DataPage.GroupBody className="h-full [&_.dg-shell]:h-full [&_.dg-table-wrapper]:min-h-0 [&_.dg-table-wrapper]:flex-1">
              <DataGrid
                data={services}
                columns={columns}
                tableWidthMode="fill-last"
                rowHeight={48}
                pagination={{ pageSize: 20 }}
                footer={(table) => (
                  <div className="flex h-9 items-center justify-between px-1 text-xs text-muted-foreground">
                    <span>{services.length} services</span>
                    <DataGridPaginationCompact table={table} />
                  </div>
                )}
              />
            </DataPage.GroupBody>
          </DataPage.Group>
        )}
      </DataPage.Content>
    </DataPage>
  )
}
