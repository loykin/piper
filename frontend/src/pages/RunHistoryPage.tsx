import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
import { listRuns, type Run } from '../api'
import StatusBadge from '../components/StatusBadge'

function elapsed(run: Run): string {
  const start = new Date(run.started_at).getTime()
  const end = run.finished_at ? new Date(run.finished_at).getTime() : Date.now()
  const ms = end - start
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  return `${(ms / 60000).toFixed(1)}m`
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
    <div className="space-y-6">
      <section className="rounded-xl border border-gray-800 bg-gray-900 p-6">
        <h1 className="text-lg font-semibold text-gray-100">Run History</h1>
        <p className="mt-1 text-sm text-gray-400">Monitor all pipeline execution records.</p>
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
