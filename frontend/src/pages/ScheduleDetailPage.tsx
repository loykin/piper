import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { getSchedule, listScheduleRuns, setScheduleEnabled, deleteSchedule, type Schedule, type Run } from '../api'
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
import RunDAG from '../components/RunDAG'
import StatusBadge from '../components/StatusBadge'

const TYPE_LABEL: Record<string, string> = {
  immediate: 'Immediate',
  once: 'Once',
  cron: 'Cron',
}

export default function ScheduleDetailPage() {
  const { id } = useParams<{ id: string }>()
  const [schedule, setSchedule] = useState<Schedule | null>(null)
  const [runs, setRuns] = useState<Run[]>([])
  const [loading, setLoading] = useState(true)
  const navigate = useNavigate()

  async function load() {
    if (!id) return
    const [sc, rs] = await Promise.all([
      getSchedule(id).catch(() => null),
      listScheduleRuns(id).catch(() => [] as Run[]),
    ])
    setSchedule(sc)
    setRuns(rs)
    setLoading(false)
  }

  useEffect(() => {
    load()
    const t = setInterval(load, 5000)
    return () => clearInterval(t)
  }, [id])

  const selected = useMemo(() => null, [])

  const runColumns: DataGridColumnDef<Run>[] = [
    {
      id: 'id',
      header: 'Run ID',
      meta: { minWidth: 200, flex: 1 },
      cell: ({ row }) => (
        <Link to={`/runs/${row.original.id}`} className="font-mono text-xs text-indigo-400 hover:underline">
          {row.original.id}
        </Link>
      ),
    },
    {
      id: 'status',
      header: 'Status',
      meta: { minWidth: 110 },
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      id: 'started_at',
      header: 'Started',
      meta: { minWidth: 180 },
      cell: ({ row }) => <span className="text-xs text-gray-400">{new Date(row.original.started_at).toLocaleString()}</span>,
    },
    {
      id: 'ended_at',
      header: 'Ended',
      meta: { minWidth: 180 },
      cell: ({ row }) => (
        <span className="text-xs text-gray-400">
          {row.original.ended_at ? new Date(row.original.ended_at).toLocaleString() : '-'}
        </span>
      ),
    },
  ]

  if (loading) return <p className="text-sm text-gray-500">Loading...</p>
  if (!schedule) return <p className="text-sm text-gray-500">Schedule not found.</p>

  const isCron = schedule.schedule_type === 'cron'

  return (
    <div className="space-y-6">
      {/* Breadcrumb */}
      <div className="flex items-center gap-3">
        <Link to="/schedules" className="text-sm text-gray-500 hover:text-gray-300">← Schedules</Link>
        <span className="text-gray-600">/</span>
        <span className="text-sm text-gray-300">{schedule.name}</span>
        <span className="rounded bg-gray-800 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-gray-400">
          {TYPE_LABEL[schedule.schedule_type] ?? schedule.schedule_type}
        </span>
      </div>

      {/* Schedule Info */}
      <section className="rounded-xl border border-gray-800 bg-gray-900 p-4">
        <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          {isCron && (
            <div>
              <dt className="text-xs text-gray-500">Cron</dt>
              <dd className="mt-1 font-mono text-sm text-gray-200">{schedule.cron_expr || '-'}</dd>
            </div>
          )}
          {schedule.schedule_type === 'once' && (
            <div>
              <dt className="text-xs text-gray-500">Scheduled At</dt>
              <dd className="mt-1 text-sm text-gray-200">{new Date(schedule.next_run_at).toLocaleString()}</dd>
            </div>
          )}
          <div>
            <dt className="text-xs text-gray-500">Status</dt>
            <dd className="mt-1 text-sm text-gray-200">
              {schedule.enabled ? (isCron ? 'Active' : 'Waiting') : (schedule.schedule_type === 'cron' ? 'Disabled' : 'Done')}
            </dd>
          </div>
          <div>
            <dt className="text-xs text-gray-500">Last Run</dt>
            <dd className="mt-1 text-sm text-gray-200">
              {schedule.last_run_at ? new Date(schedule.last_run_at).toLocaleString() : '-'}
            </dd>
          </div>
          <div>
            <dt className="text-xs text-gray-500">Created</dt>
            <dd className="mt-1 text-sm text-gray-200">{new Date(schedule.created_at).toLocaleString()}</dd>
          </div>
          <div>
            <dt className="text-xs text-gray-500">Total Runs</dt>
            <dd className="mt-1 text-sm text-gray-200">{runs.length}</dd>
          </div>
        </dl>

        <div className="mt-4 flex items-center gap-2">
          {isCron && (
            <button
              type="button"
              onClick={async () => {
                try { await setScheduleEnabled(schedule.id, !schedule.enabled); await load() } catch { /* no-op */ }
              }}
              className={`rounded border px-3 py-1.5 text-xs font-medium ${schedule.enabled ? 'border-green-700 bg-green-900/20 text-green-300' : 'border-gray-700 bg-gray-900 text-gray-400'}`}
            >
              {schedule.enabled ? 'Enabled — click to disable' : 'Disabled — click to enable'}
            </button>
          )}
          <button
            type="button"
            onClick={async () => {
              if (!confirm(`Delete schedule "${schedule.name}"?`)) return
              try { await deleteSchedule(schedule.id); navigate('/schedules') } catch { /* no-op */ }
            }}
            className="rounded border border-red-900 bg-red-950/20 px-3 py-1.5 text-xs font-medium text-red-400 hover:bg-red-900/30"
          >
            Delete
          </button>
        </div>
      </section>

      {/* DAG Preview */}
      <RunDAG
        pipelineYaml={schedule.pipeline_yaml}
        steps={[]}
        selected={selected}
        onSelectStep={() => {}}
      />

      {/* Run History */}
      <section className="overflow-hidden rounded-xl border border-gray-800 bg-gray-950">
        <div className="border-b border-gray-800 bg-gray-900 px-4 py-3">
          <h2 className="text-sm font-semibold uppercase tracking-wider text-gray-300">Run History</h2>
        </div>
        {runs.length === 0 ? (
          <div className="px-4 py-8 text-sm text-gray-500">
            {schedule.schedule_type === 'immediate' ? 'Running...' : 'No runs yet.'}
          </div>
        ) : (
          <DataGrid
            data={runs}
            columns={runColumns}
            tableHeight="auto"
            rowHeight={44}
            pagination={{ pageSize: 10 }}
            footer={(table) => <DataGridPaginationBar table={table} pageSizes={[10, 20]} />}
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

      {/* YAML */}
      <section className="overflow-hidden rounded-xl border border-gray-800 bg-gray-950">
        <div className="border-b border-gray-800 bg-gray-900 px-4 py-3">
          <h2 className="text-sm font-semibold uppercase tracking-wider text-gray-300">Pipeline YAML</h2>
        </div>
        <pre className="overflow-x-auto p-4 text-xs leading-6 text-gray-300">{schedule.pipeline_yaml || '(empty)'}</pre>
      </section>
    </div>
  )
}

