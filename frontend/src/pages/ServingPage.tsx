import { useEffect, useRef, useState } from 'react'
import { RefreshCw, Square } from 'lucide-react'
import { IconButton } from '@/components/ui/icon-button'
import { DataGrid, DataGridPaginationCompact, type DataGridColumnDef } from '@loykin/gridkit'
import {
  DataBodyTemplate, DataPage,
  Select, SelectTrigger, SelectContent, SelectItem, SelectValue,
} from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  listServing, deployServing, stopServing, restartServing,
  listServingWorkers,
  type Service, type ServingWorkerInfo,
} from '@/features/serving/api'
import { listRuns, listArtifacts, type Run, type StepArtifacts } from '@/features/runs/api'
import { serviceColumns } from '@/features/serving/columns'

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
  worker: string
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
  worker: '',
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
  const workerLine = !isK8s && f.worker ? `    worker: ${f.worker}\n` : ''

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
${workerLine}${k8sSection}`
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
  const [servingWorkers, setServingWorkers] = useState<ServingWorkerInfo[]>([])

  useEffect(() => {
    listRuns({ status: 'success' }).then(setSuccessRuns).catch(() => {})
    listServingWorkers().then(setServingWorkers).catch(() => {})
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
            <DataBodyTemplate.Field label="Service Name">
              <Input value={form.name} onChange={e => setField('name', e.target.value)} placeholder="my-model" />
            </DataBodyTemplate.Field>

            <DataBodyTemplate.Group layout="stacked" variant="bordered" title="Model Source">
              <div className="grid grid-cols-2 gap-3">
                <DataBodyTemplate.Field label="Pipeline">
                  <Select value={form.pipeline} onValueChange={v => setField('pipeline', v ?? '')}>
                    <SelectTrigger size="sm"><SelectValue placeholder="— select pipeline —" /></SelectTrigger>
                    <SelectContent>
                      {pipelines.map(p => <SelectItem key={p} value={p}>{p}</SelectItem>)}
                    </SelectContent>
                  </Select>
                </DataBodyTemplate.Field>
                <DataBodyTemplate.Field label="Run">
                  <Select value={form.run} onValueChange={v => setField('run', v ?? '')} disabled={!form.pipeline}>
                    <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
                    <SelectContent>
                      <SelectItem value="latest">latest</SelectItem>
                      {pipelineRuns.map(r => (
                        <SelectItem key={r.id} value={r.id}>
                          {r.id.slice(0, 20)}… {r.started_at ? new Date(r.started_at).toLocaleDateString() : ''}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </DataBodyTemplate.Field>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <DataBodyTemplate.Field label="Step">
                  <Select value={form.step} onValueChange={v => setField('step', v ?? '')} disabled={steps.length === 0}>
                    <SelectTrigger size="sm"><SelectValue placeholder="— select step —" /></SelectTrigger>
                    <SelectContent>
                      {steps.map(s => <SelectItem key={s} value={s}>{s}</SelectItem>)}
                    </SelectContent>
                  </Select>
                </DataBodyTemplate.Field>
                <DataBodyTemplate.Field label="Artifact">
                  <Select value={form.artifact} onValueChange={v => setField('artifact', v ?? '')} disabled={artifactNames.length === 0}>
                    <SelectTrigger size="sm"><SelectValue placeholder="— select artifact —" /></SelectTrigger>
                    <SelectContent>
                      {artifactNames.map(a => <SelectItem key={a} value={a}>{a}</SelectItem>)}
                    </SelectContent>
                  </Select>
                </DataBodyTemplate.Field>
              </div>
            </DataBodyTemplate.Group>

            <DataBodyTemplate.Group layout="stacked" variant="bordered" title="Runtime">
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
                <DataBodyTemplate.Field label="Mode">
                  <Select value={form.runtimeMode} onValueChange={v => setField('runtimeMode', v ?? '')}>
                    <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
                    <SelectContent>
                      <SelectItem value="local">local</SelectItem>
                      <SelectItem value="k8s">k8s</SelectItem>
                    </SelectContent>
                  </Select>
                </DataBodyTemplate.Field>
                <DataBodyTemplate.Field label="Port">
                  <Input value={form.port} onChange={e => setField('port', e.target.value)} placeholder="8000" />
                </DataBodyTemplate.Field>
                <DataBodyTemplate.Field label="Health Path">
                  <Input value={form.healthPath} onChange={e => setField('healthPath', e.target.value)} placeholder="/" />
                </DataBodyTemplate.Field>
              </div>

              {form.runtimeMode === 'local' && (
                <DataBodyTemplate.Field label="Worker" description="Deploy to a specific worker node. Leave blank to auto-assign.">
                  <Select value={form.worker} onValueChange={v => setField('worker', v ?? '')}>
                    <SelectTrigger size="sm"><SelectValue placeholder="— auto assign —" /></SelectTrigger>
                    <SelectContent>
                      <SelectItem value="">auto assign</SelectItem>
                      {servingWorkers.map(w => (
                        <SelectItem key={w.id} value={w.hostname}>
                          {w.hostname}{w.gpus?.length ? ` (GPU: ${w.gpus.join(', ')})` : ''}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </DataBodyTemplate.Field>
              )}

              {form.runtimeMode === 'k8s' && (
                <>
                  <DataBodyTemplate.Field label="Container Image">
                    <Input value={form.image} onChange={e => setField('image', e.target.value)} placeholder="registry/image:tag" />
                  </DataBodyTemplate.Field>
                  <div className="grid grid-cols-3 gap-3">
                    <DataBodyTemplate.Field label="Namespace">
                      <Input value={form.k8sNamespace} onChange={e => setField('k8sNamespace', e.target.value)} placeholder="default" />
                    </DataBodyTemplate.Field>
                    <DataBodyTemplate.Field label="Replicas">
                      <Input type="number" min="1" value={form.k8sReplicas} onChange={e => setField('k8sReplicas', e.target.value)} />
                    </DataBodyTemplate.Field>
                    <DataBodyTemplate.Field label="Image Pull Policy">
                      <Select value={form.k8sImagePullPolicy} onValueChange={v => setField('k8sImagePullPolicy', v ?? '')}>
                        <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
                        <SelectContent>
                          <SelectItem value="Always">Always</SelectItem>
                          <SelectItem value="IfNotPresent">IfNotPresent</SelectItem>
                          <SelectItem value="Never">Never</SelectItem>
                        </SelectContent>
                      </Select>
                    </DataBodyTemplate.Field>
                  </div>
                  <div className="grid grid-cols-3 gap-3">
                    <DataBodyTemplate.Field label="CPU"><Input value={form.k8sCPU} onChange={e => setField('k8sCPU', e.target.value)} placeholder="2" /></DataBodyTemplate.Field>
                    <DataBodyTemplate.Field label="Memory"><Input value={form.k8sMemory} onChange={e => setField('k8sMemory', e.target.value)} placeholder="4Gi" /></DataBodyTemplate.Field>
                    <DataBodyTemplate.Field label="GPU"><Input value={form.k8sGPU} onChange={e => setField('k8sGPU', e.target.value)} placeholder="1" /></DataBodyTemplate.Field>
                  </div>
                </>
              )}

              <DataBodyTemplate.Field label="Command" description="One argument per line. $PIPER_MODEL_DIR points to the artifact directory.">
                <textarea
                  className="w-full resize-none rounded border border-border bg-background px-3 py-2 font-mono text-sm focus:outline-none"
                  rows={4}
                  value={form.command}
                  onChange={e => setField('command', e.target.value)}
                  spellCheck={false}
                />
              </DataBodyTemplate.Field>
            </DataBodyTemplate.Group>
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
          <Button variant="outline" size="sm" onClick={onClose}>Cancel</Button>
          <Button size="sm" onClick={handleDeploy} disabled={deploying}>
            {deploying ? 'Deploying…' : 'Deploy'}
          </Button>
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

  const actionColumn: DataGridColumnDef<Service> = {
    id: 'actions',
    header: '',
    meta: { minWidth: 140 },
    cell: ({ row }) => {
      const svc = row.original
      return (
        <div className="flex items-center gap-0.5">
          {svc.status === 'running' && (
            <IconButton icon={<RefreshCw />} label="Restart"
              onClick={e => { e.stopPropagation(); handleRestart(svc.name) }} />
          )}
          {svc.status !== 'stopped' && (
            <IconButton icon={<Square />} label="Stop"
              onClick={e => { e.stopPropagation(); handleStop(svc.name) }}
              className="text-destructive hover:bg-destructive/10" />
          )}
        </div>
      )
    },
  }

  const columns = [...serviceColumns, actionColumn]

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
