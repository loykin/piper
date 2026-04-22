import { useEffect, useState, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
import { listRuns, createRun, type Run } from '../api'
import StatusBadge from '../components/StatusBadge'

const EXAMPLE_YAML = `apiVersion: piper/v1
kind: Pipeline
metadata:
  name: my-pipeline
spec:
  steps:
    - name: hello
      run:
        command: [echo, "hello from piper"]
    - name: world
      depends_on: [hello]
      run:
        command: [echo, "world!"]
`

function elapsed(run: Run): string {
  const start = new Date(run.started_at).getTime()
  const end = run.ended_at ? new Date(run.ended_at).getTime() : Date.now()
  const ms = end - start
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  return `${(ms / 60000).toFixed(1)}m`
}

export default function RunsPage() {
  const [runs, setRuns] = useState<Run[]>([])
  const [loading, setLoading] = useState(true)
  const [yaml, setYaml] = useState(EXAMPLE_YAML)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const navigate = useNavigate()
  const intervalRef = useRef<ReturnType<typeof setInterval>>(0 as unknown as ReturnType<typeof setInterval>)

  const load = () =>
    listRuns()
      .then(setRuns)
      .catch(() => {})
      .finally(() => setLoading(false))

  useEffect(() => {
    load()
    intervalRef.current = setInterval(load, 3000)
    return () => clearInterval(intervalRef.current!)
  }, [])

  async function handleRun() {
    setSubmitting(true)
    setError('')
    try {
      const { run_id } = await createRun(yaml)
      navigate(`/runs/${run_id}`)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSubmitting(false)
    }
  }

  const columns: DataGridColumnDef<Run>[] = [
    {
      accessorKey: 'id',
      header: 'Run ID',
      meta: { minWidth: 240 },
      cell: ({ row }) => (
        <button
          type="button"
          onClick={() => navigate(`/runs/${row.original.id}`)}
          className="font-mono text-xs text-indigo-400 hover:text-indigo-300"
        >
          {row.original.id}
        </button>
      ),
    },
    {
      accessorKey: 'pipeline_name',
      header: 'Pipeline',
      meta: { minWidth: 180, flex: 1 },
    },
    {
      accessorKey: 'status',
      header: 'Status',
      meta: { minWidth: 140 },
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      accessorKey: 'started_at',
      header: 'Started',
      meta: { minWidth: 220 },
      cell: ({ row }) => (
        <span className="text-gray-400">{new Date(row.original.started_at).toLocaleString()}</span>
      ),
    },
    {
      id: 'duration',
      header: 'Duration',
      meta: { minWidth: 120, align: 'right' },
      cell: ({ row }) => <span className="text-gray-400">{elapsed(row.original)}</span>,
    },
  ]

  return (
    <div className="space-y-8">
      <section className="rounded-xl border border-gray-800 bg-gray-900 p-6">
        <h2 className="mb-4 text-sm font-semibold uppercase tracking-wider text-gray-300">New Run</h2>
        <textarea
          className="w-full resize-none rounded-lg border border-gray-700 bg-gray-950 p-3 font-mono text-sm text-gray-200 focus:border-indigo-500 focus:outline-none"
          rows={10}
          value={yaml}
          onChange={e => setYaml(e.target.value)}
          spellCheck={false}
        />
        {error && <p className="mt-2 text-sm text-red-400">{error}</p>}
        <div className="mt-3 flex justify-end">
          <button
            onClick={handleRun}
            disabled={submitting}
            className="rounded-lg bg-indigo-600 px-5 py-2 text-sm font-semibold text-white transition-colors hover:bg-indigo-500 disabled:opacity-50"
          >
            {submitting ? 'Submitting…' : '▶ Run Pipeline'}
          </button>
        </div>
      </section>

      <section>
        <h2 className="mb-4 text-sm font-semibold uppercase tracking-wider text-gray-300">Recent Runs</h2>
        {loading ? (
          <p className="text-sm text-gray-500">Loading…</p>
        ) : runs.length === 0 ? (
          <p className="text-sm text-gray-500">No runs yet.</p>
        ) : (
          <div className="overflow-hidden rounded-xl border border-gray-800 bg-gray-950">
            <DataGrid
              data={runs}
              columns={columns}
              tableHeight="auto"
              rowHeight={44}
              rowCursor
              onRowClick={(row) => navigate(`/runs/${row.id}`)}
              pagination={{ pageSize: 10 }}
              footer={(table) => <DataGridPaginationBar table={table} pageSizes={[10, 20, 50]} />}
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
