import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
import { listRuns, type Run, type Step } from '../api'
import StatusBadge from '../components/StatusBadge'

function elapsed(run: Run): string {
  const start = new Date(run.started_at).getTime()
  const end = run.ended_at ? new Date(run.ended_at).getTime() : Date.now()
  const ms = end - start
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  return `${(ms / 60000).toFixed(1)}m`
}

const STEP_COLOR: Record<string, string> = {
  done:    'bg-green-500',
  success: 'bg-green-500',
  running: 'bg-blue-400 animate-pulse',
  failed:  'bg-red-500',
  skipped: 'bg-yellow-600',
  pending: 'bg-gray-600',
}

function StepDots({ steps }: { steps: Step[] }) {
  if (!steps.length) return <span className="text-xs text-gray-600">—</span>
  return (
    <div className="flex flex-wrap items-center gap-1">
      {steps.map((s) => (
        <span
          key={s.step_name}
          title={`${s.step_name}: ${s.status}`}
          className={`inline-block h-3.5 w-3.5 rounded-sm ${STEP_COLOR[s.status] ?? 'bg-gray-600'}`}
        />
      ))}
    </div>
  )
}

export default function RunHistoryPage() {
  const [runs, setRuns] = useState<Run[]>([])
  const [loading, setLoading] = useState(true)
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
    return () => clearInterval(intervalRef.current)
  }, [])

  const columns: DataGridColumnDef<Run>[] = [
    {
      accessorKey: 'id',
      header: 'Run ID',
      meta: { minWidth: 240 },
      cell: ({ row }) => (
        <span className="font-mono text-xs text-indigo-400">
          {row.original.id}
        </span>
      ),
    },
    {
      accessorKey: 'pipeline_name',
      header: 'Pipeline',
      meta: { minWidth: 160, flex: 1 },
    },
    {
      accessorKey: 'status',
      header: 'Status',
      meta: { minWidth: 120 },
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      id: 'steps',
      header: 'Steps',
      meta: { minWidth: 160 },
      cell: ({ row }) => <StepDots steps={row.original.steps ?? []} />,
    },
    {
      accessorKey: 'started_at',
      header: 'Started',
      meta: { minWidth: 200 },
      cell: ({ row }) => (
        <span className="text-gray-400">{new Date(row.original.started_at).toLocaleString()}</span>
      ),
    },
    {
      id: 'duration',
      header: 'Duration',
      meta: { minWidth: 100 },
      cell: ({ row }) => <span className="text-gray-400">{elapsed(row.original)}</span>,
    },
  ]

  return (
    <div className="space-y-6">
      <section className="rounded-xl border border-gray-800 bg-gray-900 p-6">
        <h1 className="text-lg font-semibold text-gray-100">Run History</h1>
        <p className="mt-1 text-sm text-gray-400">All pipeline runs. Each square in Steps represents one step's status.</p>
      </section>

      <section>
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
              pagination={{ pageSize: 20 }}
              footer={(table) => <DataGridPaginationBar table={table} pageSizes={[20, 50, 100]} />}
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
