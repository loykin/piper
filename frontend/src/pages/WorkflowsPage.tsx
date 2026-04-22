import { useEffect, useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
import { listSchedules, setScheduleEnabled, deleteSchedule, type Schedule } from '../api'

export default function WorkflowsPage() {
  const [schedules, setSchedules] = useState<Schedule[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const navigate = useNavigate()
  const intervalRef = useRef<ReturnType<typeof setInterval>>(0 as unknown as ReturnType<typeof setInterval>)

  const load = () =>
    listSchedules()
      .then((data) => {
        setSchedules(data)
        setLoadError('')
      })
      .catch(() => setLoadError('Failed to load schedules. Check backend status.'))
      .finally(() => setLoading(false))

  useEffect(() => {
    load()
    intervalRef.current = setInterval(load, 5000)
    return () => clearInterval(intervalRef.current)
  }, [])

  const columns: DataGridColumnDef<Schedule>[] = [
    {
      accessorKey: 'name',
      header: 'Name',
      meta: { minWidth: 200, flex: 1 },
    },
    {
      id: 'type',
      header: 'Type',
      meta: { minWidth: 100 },
      cell: ({ row }) => (
        <span
          className={`rounded px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide ${row.original.schedule_type === 'cron' ? 'border border-emerald-700 bg-emerald-900/30 text-emerald-300' : 'border border-violet-700 bg-violet-900/30 text-violet-300'}`}
        >
          {row.original.schedule_type === 'cron' ? 'Cron' : 'Once'}
        </span>
      ),
    },
    {
      id: 'schedule',
      header: 'Schedule',
      meta: { minWidth: 200 },
      cell: ({ row }) => (
        <span className="font-mono text-xs text-gray-300">
          {row.original.schedule_type === 'cron'
            ? row.original.cron_expr || '-'
            : new Date(row.original.next_run_at).toLocaleString()}
        </span>
      ),
    },
    {
      id: 'next_run_at',
      header: 'Next Run',
      meta: { minWidth: 180 },
      cell: ({ row }) => (
        <span className="text-xs text-gray-400">
          {row.original.schedule_type === 'cron'
            ? new Date(row.original.next_run_at).toLocaleString()
            : row.original.enabled ? new Date(row.original.next_run_at).toLocaleString() : 'Done'}
        </span>
      ),
    },
    {
      id: 'last_run_at',
      header: 'Last Run',
      meta: { minWidth: 180 },
      cell: ({ row }) => (
        <span className="text-xs text-gray-400">
          {row.original.last_run_at ? new Date(row.original.last_run_at).toLocaleString() : '-'}
        </span>
      ),
    },
    {
      id: 'created_at',
      header: 'Created',
      meta: { minWidth: 180 },
      cell: ({ row }) => (
        <span className="text-xs text-gray-400">{new Date(row.original.created_at).toLocaleString()}</span>
      ),
    },
    {
      id: 'actions',
      header: 'Actions',
      meta: { minWidth: 220 },
      cell: ({ row }) => {
        const s = row.original
        return (
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => navigate(`/schedules/${s.id}`)}
              className="rounded border border-gray-700 bg-gray-900 px-2 py-1 text-xs text-gray-300 hover:bg-gray-800"
            >
              View
            </button>
            {s.schedule_type === 'cron' && (
              <button
                type="button"
                onClick={async () => {
                  try { await setScheduleEnabled(s.id, !s.enabled); await load() } catch { /* no-op */ }
                }}
                className={`rounded border px-2 py-1 text-xs ${s.enabled ? 'border-green-700 bg-green-900/20 text-green-300' : 'border-gray-700 bg-gray-900 text-gray-400'}`}
              >
                {s.enabled ? 'Enabled' : 'Disabled'}
              </button>
            )}
            <button
              type="button"
              onClick={async () => {
                if (!confirm(`Delete schedule "${s.name}"?`)) return
                try { await deleteSchedule(s.id); await load() } catch { /* no-op */ }
              }}
              className="rounded border border-red-900 bg-red-950/20 px-2 py-1 text-xs text-red-400 hover:bg-red-900/30"
            >
              Delete
            </button>
          </div>
        )
      },
    },
  ]

  return (
    <div className="space-y-6">
      <section className="rounded-xl border border-gray-800 bg-gray-900 p-6">
        <div className="flex items-center justify-between gap-4">
          <div>
            <h1 className="text-lg font-semibold text-gray-100">Schedules</h1>
            <p className="mt-1 text-sm text-gray-400">Manage cron and one-time pipeline schedules.</p>
          </div>
          <Link
            to="/schedules/create"
            className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-indigo-500"
          >
            + Create
          </Link>
        </div>
      </section>

      <section className="overflow-hidden rounded-xl border border-gray-800 bg-gray-950">
        {loadError && <div className="border-b border-amber-900 bg-amber-950/30 px-4 py-2 text-xs text-amber-300">{loadError}</div>}

        {loading ? (
          <div className="px-4 py-8 text-sm text-gray-500">Loading...</div>
        ) : schedules.length === 0 ? (
          <div className="px-4 py-8 text-sm text-gray-500">No schedules yet. Create one to start.</div>
        ) : (
          <DataGrid
            data={schedules}
            columns={columns}
            tableHeight="auto"
            rowHeight={44}
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
        )}
      </section>
    </div>
  )
}

