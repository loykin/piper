import { useEffect, useRef, useState } from 'react'
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
import { listServices, deployService, stopService, restartService, type Service } from '../api'

const STATUS_COLOR: Record<string, string> = {
  running: 'bg-green-500/20 text-green-300 border border-green-500/30',
  stopped: 'bg-gray-500/20 text-gray-400 border border-gray-600/30',
  failed:  'bg-red-500/20 text-red-300 border border-red-500/30',
}

const EXAMPLE_YAML = `apiVersion: piper/v1
kind: ModelService
metadata:
  name: my-model
spec:
  model:
    from_artifact:
      pipeline: my-pipeline  # pipeline metadata.name
      step: train            # step that produced the artifact
      artifact: model        # outputs[].name in that step
      run: latest            # "latest" = most recent successful run
  runtime:
    mode: local              # local process (no k8s needed)
    command:
      - python
      - serve.py             # run from inside $PIPER_MODEL_DIR
    port: 8000               # port the process listens on
`

export default function ServicesPage() {
  const [services, setServices] = useState<Service[]>([])
  const [loading, setLoading] = useState(true)
  const [showDeploy, setShowDeploy] = useState(false)
  const [yaml, setYaml] = useState(EXAMPLE_YAML)
  const [deploying, setDeploying] = useState(false)
  const [deployError, setDeployError] = useState('')
  const intervalRef = useRef<ReturnType<typeof setInterval>>(0 as unknown as ReturnType<typeof setInterval>)

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

  async function handleDeploy() {
    setDeployError('')
    if (!yaml.trim()) { setDeployError('YAML is required.'); return }
    try {
      setDeploying(true)
      await deployService(yaml.trim())
      setShowDeploy(false)
      setYaml(EXAMPLE_YAML)
      await load()
    } catch (e: unknown) {
      setDeployError(e instanceof Error ? e.message : String(e))
    } finally {
      setDeploying(false)
    }
  }

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
      cell: ({ row }) => (
        <span className="font-medium text-gray-100">{row.original.name}</span>
      ),
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
      cell: ({ row }) => (
        <span className="font-mono text-xs text-gray-400">{row.original.artifact || '—'}</span>
      ),
    },
    {
      accessorKey: 'endpoint',
      header: 'Endpoint',
      meta: { minWidth: 200 },
      cell: ({ row }) => {
        const ep = row.original.endpoint
        return ep ? (
          <a href={ep} target="_blank" rel="noreferrer" className="font-mono text-xs text-indigo-400 hover:underline">
            {ep}
          </a>
        ) : <span className="text-xs text-gray-600">—</span>
      },
    },
    {
      accessorKey: 'updated_at',
      header: 'Last Updated',
      meta: { minWidth: 180 },
      cell: ({ row }) => (
        <span className="text-xs text-gray-400">{new Date(row.original.updated_at).toLocaleString()}</span>
      ),
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
              <button
                type="button"
                onClick={(e) => { e.stopPropagation(); handleRestart(svc.name) }}
                className="rounded border border-gray-700 px-2 py-0.5 text-xs text-gray-300 hover:bg-gray-800"
              >
                Restart
              </button>
            )}
            {svc.status !== 'stopped' && (
              <button
                type="button"
                onClick={(e) => { e.stopPropagation(); handleStop(svc.name) }}
                className="rounded border border-red-900 px-2 py-0.5 text-xs text-red-400 hover:bg-red-950/30"
              >
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
      {/* Header */}
      <section className="flex items-start justify-between rounded-xl border border-gray-800 bg-gray-900 p-6">
        <div>
          <h1 className="text-lg font-semibold text-gray-100">Services</h1>
          <p className="mt-1 text-sm text-gray-400">
            Model serving endpoints deployed from pipeline artifacts.
          </p>
        </div>
        <button
          type="button"
          onClick={() => { setShowDeploy(true); setDeployError('') }}
          className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-semibold text-white hover:bg-indigo-500"
        >
          Deploy Service
        </button>
      </section>

      {/* Deploy panel */}
      {showDeploy && (
        <section className="rounded-xl border border-indigo-500/30 bg-gray-950 p-6">
          <h2 className="mb-4 text-sm font-semibold text-gray-200">Deploy ModelService</h2>
          <p className="mb-3 text-xs text-gray-500">
            Paste a <code className="text-gray-400">kind: ModelService</code> YAML.
            The artifact is resolved from the referenced pipeline run's output directory.
          </p>

          {/* Artifact path explanation */}
          <div className="mb-4 rounded-lg border border-gray-800 bg-gray-900 p-3 text-xs text-gray-400 leading-5">
            <span className="font-semibold text-gray-300">How it works (local mode):</span>
            <br />
            1. Resolves artifact path: <code className="text-indigo-300">piper-outputs/&#123;run_id&#125;/&#123;step&#125;/&#123;artifact&#125;</code>
            <br />
            2. Starts <code className="text-indigo-300">runtime.command</code> as a local process with <code className="text-indigo-300">PIPER_MODEL_DIR</code> env set to that path
            <br />
            3. Registers <code className="text-indigo-300">http://localhost:&#123;port&#125;</code> as the endpoint — no k8s required
            <br />
            Use <code className="text-indigo-300">run: latest</code> to always pick the most recent successful run of the pipeline.
          </div>

          <textarea
            className="w-full resize-none rounded-lg border border-gray-700 bg-gray-900 p-3 font-mono text-sm text-gray-100 focus:border-indigo-500 focus:outline-none"
            rows={16}
            value={yaml}
            onChange={(e) => setYaml(e.target.value)}
            spellCheck={false}
          />

          {deployError && <p className="mt-2 text-sm text-red-400">{deployError}</p>}

          <div className="mt-4 flex items-center justify-end gap-3">
            <button
              type="button"
              onClick={() => { setShowDeploy(false); setDeployError('') }}
              className="rounded-lg border border-gray-700 px-4 py-2 text-sm font-medium text-gray-300 hover:bg-gray-900"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={handleDeploy}
              disabled={deploying}
              className="rounded-lg bg-indigo-600 px-5 py-2 text-sm font-semibold text-white hover:bg-indigo-500 disabled:opacity-50"
            >
              {deploying ? 'Deploying...' : 'Deploy'}
            </button>
          </div>
        </section>
      )}

      {/* Service list */}
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
