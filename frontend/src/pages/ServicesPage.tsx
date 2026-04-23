import { useEffect, useRef, useState } from 'react'
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
import {
  listServices, deployService, stopService, restartService,
  listRuns, listArtifacts,
  type Service, type Run, type StepArtifacts,
} from '../api'

const STATUS_COLOR: Record<string, string> = {
  running: 'bg-green-500/20 text-green-300 border border-green-500/30',
  stopped: 'bg-gray-500/20 text-gray-400 border border-gray-600/30',
  failed:  'bg-red-500/20 text-red-300 border border-red-500/30',
}

// ─── Runtime templates ────────────────────────────────────────────────────────

interface RuntimeTemplate {
  label: string
  description: string
  runtimeMode: string
  image: string         // default container image (k8s)
  imageHint: string     // example alternative tag shown as hint
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
    imageOptions: [],
    command: 'python\nserve.py',
    port: '8000',
    healthPath: '/',
  },
  triton: {
    label: 'Triton Inference Server',
    description: 'NVIDIA Triton — gRPC/HTTP inference server (local)',
    runtimeMode: 'local',
    image: '',
    imageOptions: [],
    command: 'tritonserver\n--model-repository=$PIPER_MODEL_DIR\n--http-port=8000',
    port: '8000',
    healthPath: '/v2/health/ready',
  },
  torchserve: {
    label: 'TorchServe',
    description: 'PyTorch model server (local)',
    runtimeMode: 'local',
    image: '',
    imageOptions: [],
    command: 'torchserve\n--start\n--model-store=$PIPER_MODEL_DIR\n--foreground',
    port: '8080',
    healthPath: '/ping',
  },
  mlflow: {
    label: 'MLflow Models',
    description: 'MLflow built-in model server (local)',
    runtimeMode: 'local',
    image: '',
    imageOptions: [],
    command: 'mlflow\nmodels\nserve\n-m $PIPER_MODEL_DIR\n--port=5000\n--no-conda',
    port: '5000',
    healthPath: '/health',
  },
  bentoml: {
    label: 'BentoML',
    description: 'BentoML serving runtime (local)',
    runtimeMode: 'local',
    image: '',
    imageOptions: [],
    command: 'bentoml\nserve\n$PIPER_MODEL_DIR\n--port=3000',
    port: '3000',
    healthPath: '/healthz',
  },
  vllm: {
    label: 'vLLM',
    description: 'High-throughput LLM inference engine (local)',
    runtimeMode: 'local',
    image: '',
    imageOptions: [],
    command: 'python\n-m vllm.entrypoints.openai.api_server\n--model=$PIPER_MODEL_DIR\n--port=8000',
    port: '8000',
    healthPath: '/health',
  },
  ollama: {
    label: 'Ollama',
    description: 'Local LLM runtime',
    runtimeMode: 'local',
    image: '',
    imageOptions: [],
    command: 'ollama\nserve',
    port: '11434',
    healthPath: '/api/version',
  },
  triton_k8s: {
    label: 'Triton (k8s)',
    description: 'NVIDIA Triton on Kubernetes — model repo from S3',
    runtimeMode: 'k8s',
    image: 'nvcr.io/nvidia/tritonserver:24.12-py3',
    imageOptions: [
      'nvcr.io/nvidia/tritonserver:24.12-py3',
      'nvcr.io/nvidia/tritonserver:24.09-py3',
      'nvcr.io/nvidia/tritonserver:24.12-trtllm-python-py3',
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
    imageOptions: [
      'vllm/vllm-openai:latest',
      'vllm/vllm-openai:v0.6.6',
      'vllm/vllm-openai:v0.5.5',
    ],
    command: 'python\n-m vllm.entrypoints.openai.api_server\n--model=$PIPER_MODEL_DIR\n--port=8000',
    port: '8000',
    healthPath: '/health',
  },
  torchserve_k8s: {
    label: 'TorchServe (k8s)',
    description: 'PyTorch TorchServe on Kubernetes',
    runtimeMode: 'k8s',
    image: 'pytorch/torchserve:latest-gpu',
    imageOptions: [
      'pytorch/torchserve:latest-gpu',
      'pytorch/torchserve:latest',
      'pytorch/torchserve:0.11.1-gpu',
    ],
    command: 'torchserve\n--start\n--model-store=$PIPER_MODEL_DIR\n--foreground',
    port: '8080',
    healthPath: '/ping',
  },
  mlflow_k8s: {
    label: 'MLflow (k8s)',
    description: 'MLflow model server on Kubernetes',
    runtimeMode: 'k8s',
    image: 'ghcr.io/mlflow/mlflow:v2.19.0',
    imageOptions: [
      'ghcr.io/mlflow/mlflow:v2.19.0',
      'ghcr.io/mlflow/mlflow:v2.18.0',
      'ghcr.io/mlflow/mlflow:latest',
    ],
    command: 'mlflow\nmodels\nserve\n-m $PIPER_MODEL_DIR\n--port=5000\n--no-conda\n--host=0.0.0.0',
    port: '5000',
    healthPath: '/health',
  },
}

// ─── YAML builder ─────────────────────────────────────────────────────────────

interface FormState {
  name: string
  pipeline: string
  run: string       // "latest" or a run ID
  step: string
  artifact: string
  templateKey: string
  runtimeMode: string
  image: string     // k8s only: container image
  command: string   // one argument per line
  port: string
  healthPath: string
  // k8s spec
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

  // Build optional k8s section
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

interface DeployPanelProps {
  form: FormState
  setForm: React.Dispatch<React.SetStateAction<FormState>>
  tab: 'form' | 'yaml'
  setTab: React.Dispatch<React.SetStateAction<'form' | 'yaml'>>
  yaml: string
  setYaml: React.Dispatch<React.SetStateAction<string>>
  showPreview: boolean
  setShowPreview: React.Dispatch<React.SetStateAction<boolean>>
  onClose: () => void
  onDeployed: () => void
}

function DeployPanel({
  form, setForm, tab, setTab, yaml, setYaml, showPreview, setShowPreview,
  onClose, onDeployed,
}: DeployPanelProps) {
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
      // If user manually edits runtime fields, switch template to "custom"
      if (key === 'command' || key === 'port' || key === 'healthPath') {
        next.templateKey = 'custom'
      }
      setYaml(buildYAML(next))
      return next
    })
  }

  function handleTabSwitch(t: 'form' | 'yaml') {
    if (t === 'yaml') setYaml(buildYAML(form))
    setTab(t)
  }

  async function handleDeploy() {
    setError('')
    const payload = tab === 'form' ? buildYAML(form) : yaml
    if (!payload.trim()) { setError('YAML is required.'); return }
    try {
      setDeploying(true)
      await deployService(payload.trim())
      onDeployed()
      onClose()
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setDeploying(false)
    }
  }

  const inputCls = 'w-full rounded-lg border border-gray-700 bg-gray-900 px-3 py-2 text-sm text-gray-100 focus:border-indigo-500 focus:outline-none'
  const selectCls = inputCls
  const labelCls = 'block text-xs font-medium text-gray-400 mb-1'

  return (
    <section className="rounded-xl border border-indigo-500/30 bg-gray-950 p-6">
      <div className="mb-5 flex items-center justify-between">
        <h2 className="text-sm font-semibold text-gray-200">Deploy ModelService</h2>
        <div className="flex overflow-hidden rounded-lg border border-gray-700 text-xs">
          <button type="button" onClick={() => handleTabSwitch('form')}
            className={`px-3 py-1.5 ${tab === 'form' ? 'bg-indigo-600 text-white' : 'bg-gray-900 text-gray-400 hover:bg-gray-800'}`}>
            Form
          </button>
          <button type="button" onClick={() => handleTabSwitch('yaml')}
            className={`px-3 py-1.5 ${tab === 'yaml' ? 'bg-indigo-600 text-white' : 'bg-gray-900 text-gray-400 hover:bg-gray-800'}`}>
            YAML
          </button>
        </div>
      </div>

      {tab === 'form' ? (
        <div className="space-y-5">
          <div>
            <label className={labelCls}>Service Name</label>
            <input className={inputCls} value={form.name} onChange={e => setField('name', e.target.value)} placeholder="my-model" />
          </div>

          <fieldset className="space-y-4 rounded-lg border border-gray-800 p-4">
            <legend className="px-1 text-xs font-semibold text-gray-400">Model Source</legend>
            <div className="grid grid-cols-2 gap-4">
              <div>
                <label className={labelCls}>Pipeline</label>
                <select className={selectCls} value={form.pipeline} onChange={e => setField('pipeline', e.target.value)}>
                  <option value="">— select pipeline —</option>
                  {pipelines.map(p => <option key={p} value={p}>{p}</option>)}
                </select>
                {pipelines.length === 0 && (
                  <p className="mt-1 text-xs text-gray-600">No successful runs found.</p>
                )}
              </div>
              <div>
                <label className={labelCls}>Run</label>
                <select className={selectCls} value={form.run} onChange={e => setField('run', e.target.value)} disabled={!form.pipeline}>
                  <option value="latest">latest (most recent success)</option>
                  {pipelineRuns.map(r => (
                    <option key={r.id} value={r.id}>
                      {r.id.slice(0, 20)}… {r.started_at ? new Date(r.started_at).toLocaleDateString() : ''}
                    </option>
                  ))}
                </select>
              </div>
            </div>
            <div className="grid grid-cols-2 gap-4">
              <div>
                <label className={labelCls}>Step</label>
                <select className={selectCls} value={form.step} onChange={e => setField('step', e.target.value)}
                  disabled={steps.length === 0}>
                  <option value="">— select step —</option>
                  {steps.map(s => <option key={s} value={s}>{s}</option>)}
                </select>
              </div>
              <div>
                <label className={labelCls}>Artifact</label>
                <select className={selectCls} value={form.artifact} onChange={e => setField('artifact', e.target.value)}
                  disabled={artifactNames.length === 0}>
                  <option value="">— select artifact —</option>
                  {artifactNames.map(a => <option key={a} value={a}>{a}</option>)}
                </select>
              </div>
            </div>
          </fieldset>

          <fieldset className="space-y-4 rounded-lg border border-gray-800 p-4">
            <legend className="px-1 text-xs font-semibold text-gray-400">Runtime</legend>

            {/* Template preset */}
            <div>
              <label className={labelCls}>Template</label>
              <div className="flex flex-wrap gap-2">
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
                    className={`rounded-full border px-3 py-1 text-xs font-medium transition-colors ${
                      form.templateKey === key
                        ? 'border-indigo-500 bg-indigo-600/20 text-indigo-300'
                        : 'border-gray-700 bg-gray-900 text-gray-400 hover:border-gray-500 hover:text-gray-200'
                    }`}
                  >
                    {tpl.label}
                  </button>
                ))}
              </div>
              {form.templateKey !== 'custom' && (
                <p className="mt-1.5 text-xs text-gray-600">{RUNTIME_TEMPLATES[form.templateKey].description}</p>
              )}
            </div>

            <div className="grid grid-cols-3 gap-4">
              <div>
                <label className={labelCls}>Mode</label>
                <select className={selectCls} value={form.runtimeMode} onChange={e => setField('runtimeMode', e.target.value)}>
                  <option value="local">local (subprocess)</option>
                  <option value="k8s">k8s (Deployment)</option>
                </select>
              </div>
              <div>
                <label className={labelCls}>Port</label>
                <input className={inputCls} value={form.port} onChange={e => setField('port', e.target.value)} placeholder="8000" />
              </div>
              <div>
                <label className={labelCls}>Health Path</label>
                <input className={inputCls} value={form.healthPath} onChange={e => setField('healthPath', e.target.value)} placeholder="/" />
              </div>
            </div>

            {/* k8s-only fields */}
            {form.runtimeMode === 'k8s' && (
              <>
                <div>
                  <label className={labelCls}>Container Image <span className="text-red-400">*</span></label>
                  <input
                    className={inputCls}
                    value={form.image}
                    onChange={e => setField('image', e.target.value)}
                    placeholder="registry/image:tag"
                  />
                  <p className="mt-1 text-xs text-gray-600">
                    템플릿 기본값이 채워집니다. 다른 버전은 직접 수정하세요. (예: <code className="text-indigo-400">{RUNTIME_TEMPLATES[form.templateKey]?.imageOptions?.[1] ?? 'registry/image:other-tag'}</code>)
                  </p>
                </div>
                <div className="grid grid-cols-3 gap-4">
                  <div>
                    <label className={labelCls}>Namespace</label>
                    <input className={inputCls} value={form.k8sNamespace} onChange={e => setField('k8sNamespace', e.target.value)} placeholder="default" />
                  </div>
                  <div>
                    <label className={labelCls}>Replicas</label>
                    <input className={inputCls} type="number" min="1" value={form.k8sReplicas} onChange={e => setField('k8sReplicas', e.target.value)} placeholder="1" />
                  </div>
                  <div>
                    <label className={labelCls}>Image Pull Policy</label>
                    <select className={selectCls} value={form.k8sImagePullPolicy} onChange={e => setField('k8sImagePullPolicy', e.target.value)}>
                      <option value="Always">Always</option>
                      <option value="IfNotPresent">IfNotPresent</option>
                      <option value="Never">Never</option>
                    </select>
                  </div>
                </div>
                <div className="grid grid-cols-3 gap-4">
                  <div>
                    <label className={labelCls}>CPU Request/Limit</label>
                    <input className={inputCls} value={form.k8sCPU} onChange={e => setField('k8sCPU', e.target.value)} placeholder="2" />
                    <p className="mt-0.5 text-xs text-gray-600">e.g. 2, 500m</p>
                  </div>
                  <div>
                    <label className={labelCls}>Memory Request/Limit</label>
                    <input className={inputCls} value={form.k8sMemory} onChange={e => setField('k8sMemory', e.target.value)} placeholder="4Gi" />
                  </div>
                  <div>
                    <label className={labelCls}>GPU Limit</label>
                    <input className={inputCls} value={form.k8sGPU} onChange={e => setField('k8sGPU', e.target.value)} placeholder="1" />
                    <p className="mt-0.5 text-xs text-gray-600">nvidia.com/gpu</p>
                  </div>
                </div>
              </>
            )}
            <div>
              <label className={labelCls}>
                Command <span className="text-gray-600">(one argument per line)</span>
              </label>
              <textarea className={`${inputCls} resize-none font-mono`} rows={4}
                value={form.command} onChange={e => setField('command', e.target.value)}
                placeholder={'python\nserve.py'} spellCheck={false} />
              <p className="mt-1 text-xs text-gray-600">
                <code className="text-indigo-400">$PIPER_MODEL_DIR</code> points to the artifact directory at runtime
              </p>
            </div>
          </fieldset>

          <div>
            <button type="button" onClick={() => setShowPreview(v => !v)}
              className="flex items-center gap-1 text-xs text-gray-500 hover:text-gray-300">
              <span>{showPreview ? '▾' : '▸'}</span> Preview YAML
            </button>
            {showPreview && (
              <pre className="mt-2 overflow-x-auto rounded-lg border border-gray-800 bg-gray-900 p-3 font-mono text-xs text-gray-300">
                {buildYAML(form)}
              </pre>
            )}
          </div>
        </div>
      ) : (
        <textarea
          className="w-full resize-none rounded-lg border border-gray-700 bg-gray-900 p-3 font-mono text-sm text-gray-100 focus:border-indigo-500 focus:outline-none"
          rows={20}
          value={yaml}
          onChange={e => setYaml(e.target.value)}
          spellCheck={false}
        />
      )}

      {error && <p className="mt-3 text-sm text-red-400">{error}</p>}

      <div className="mt-5 flex items-center justify-end gap-3">
        <button type="button" onClick={onClose}
          className="rounded-lg border border-gray-700 px-4 py-2 text-sm font-medium text-gray-300 hover:bg-gray-900">
          Cancel
        </button>
        <button type="button" onClick={handleDeploy} disabled={deploying}
          className="rounded-lg bg-indigo-600 px-5 py-2 text-sm font-semibold text-white hover:bg-indigo-500 disabled:opacity-50">
          {deploying ? 'Deploying…' : 'Deploy'}
        </button>
      </div>
    </section>
  )
}

// ─── Main page ────────────────────────────────────────────────────────────────

export default function ServicesPage() {
  const [services, setServices] = useState<Service[]>([])
  const [loading, setLoading] = useState(true)
  const [showDeploy, setShowDeploy] = useState(false)
  const intervalRef = useRef<ReturnType<typeof setInterval>>(0 as unknown as ReturnType<typeof setInterval>)

  // Lifted deploy form state — persists across open/close
  const [deployForm, setDeployForm] = useState<FormState>(DEFAULT_FORM)
  const [deployTab, setDeployTab] = useState<'form' | 'yaml'>('form')
  const [deployYaml, setDeployYaml] = useState(() => buildYAML(DEFAULT_FORM))
  const [deployShowPreview, setDeployShowPreview] = useState(false)

  const load = () =>
    listServices()
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
    try { await stopService(name); await load() } catch { /* no-op */ }
  }

  async function handleRestart(name: string) {
    try { await restartService(name); await load() } catch { /* no-op */ }
  }

  const columns: DataGridColumnDef<Service>[] = [
    {
      accessorKey: 'name',
      header: 'Name',
      meta: { minWidth: 180, flex: 1 },
      cell: ({ row }) => <span className="font-medium text-gray-100">{row.original.name}</span>,
    },
    {
      accessorKey: 'status',
      header: 'Status',
      meta: { minWidth: 110 },
      cell: ({ row }) => (
        <span className={`inline-flex items-center rounded px-2 py-0.5 text-xs font-semibold ${STATUS_COLOR[row.original.status] ?? ''}`}>
          {row.original.status}
        </span>
      ),
    },
    {
      accessorKey: 'artifact',
      header: 'Artifact',
      meta: { minWidth: 160 },
      cell: ({ row }) => <span className="font-mono text-xs text-gray-400">{row.original.artifact || '—'}</span>,
    },
    {
      accessorKey: 'endpoint',
      header: 'Endpoint',
      meta: { minWidth: 200 },
      cell: ({ row }) => {
        const ep = row.original.endpoint
        return ep ? (
          <a href={ep} target="_blank" rel="noreferrer" className="font-mono text-xs text-indigo-400 hover:underline">{ep}</a>
        ) : <span className="text-xs text-gray-600">—</span>
      },
    },
    {
      accessorKey: 'updated_at',
      header: 'Last Updated',
      meta: { minWidth: 180 },
      cell: ({ row }) => <span className="text-xs text-gray-400">{new Date(row.original.updated_at).toLocaleString()}</span>,
    },
    {
      id: 'actions',
      header: '',
      meta: { minWidth: 160 },
      cell: ({ row }) => {
        const svc = row.original
        return (
          <div className="flex items-center gap-2">
            {svc.status === 'running' && (
              <button type="button" onClick={(e) => { e.stopPropagation(); handleRestart(svc.name) }}
                className="rounded border border-gray-700 px-2 py-0.5 text-xs text-gray-300 hover:bg-gray-800">
                Restart
              </button>
            )}
            {svc.status !== 'stopped' && (
              <button type="button" onClick={(e) => { e.stopPropagation(); handleStop(svc.name) }}
                className="rounded border border-red-900 px-2 py-0.5 text-xs text-red-400 hover:bg-red-950/30">
                Stop
              </button>
            )}
          </div>
        )
      },
    },
  ]

  return (
    <div className="space-y-6">
      <section className="flex items-start justify-between rounded-xl border border-gray-800 bg-gray-900 p-6">
        <div>
          <h1 className="text-lg font-semibold text-gray-100">Services</h1>
          <p className="mt-1 text-sm text-gray-400">Model serving endpoints deployed from pipeline artifacts.</p>
        </div>
        {!showDeploy && (
          <button type="button" onClick={() => setShowDeploy(true)}
            className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-semibold text-white hover:bg-indigo-500">
            Deploy Service
          </button>
        )}
      </section>

      {showDeploy && (
        <DeployPanel
          form={deployForm} setForm={setDeployForm}
          tab={deployTab} setTab={setDeployTab}
          yaml={deployYaml} setYaml={setDeployYaml}
          showPreview={deployShowPreview} setShowPreview={setDeployShowPreview}
          onClose={() => setShowDeploy(false)}
          onDeployed={load}
        />
      )}

      <section>
        {loading ? (
          <p className="text-sm text-gray-500">Loading…</p>
        ) : services.length === 0 ? (
          <div className="rounded-xl border border-gray-800 bg-gray-950 px-6 py-12 text-center">
            <p className="text-sm text-gray-500">No services deployed yet.</p>
            <p className="mt-1 text-xs text-gray-600">
              Deploy a ModelService to serve model artifacts from a pipeline run.
            </p>
          </div>
        ) : (
          <div className="overflow-hidden rounded-xl border border-gray-800 bg-gray-950">
            <DataGrid
              data={services}
              columns={columns}
              tableHeight="auto"
              rowHeight={48}
              pagination={{ pageSize: 20 }}
              footer={(table) => <DataGridPaginationBar table={table} pageSizes={[20, 50]} />}
              classNames={{
                container: 'border-0 rounded-none bg-transparent',
                header: 'bg-gray-900',
                headerCell: 'text-xs uppercase tracking-wider text-gray-400',
                row: 'bg-gray-950 hover:bg-gray-900 transition-colors',
                cell: 'text-sm text-gray-200',
              }}
            />
          </div>
        )}
      </section>
    </div>
  )
}
