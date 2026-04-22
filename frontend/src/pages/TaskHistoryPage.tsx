import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
import { getRun, listRuns, type Step } from '../api'
import StatusBadge from '../components/StatusBadge'

interface TaskHistoryRow {
  runId: string
  pipelineName: string
  triggerType: 'manual' | 'schedule'
  stepName: string
  status: Step['status']
  startedAt?: string
  endedAt?: string
  attempts: number
  error?: string
}

function formatTime(value?: string): string {
  if (!value) return '-'
  return new Date(value).toLocaleString()
}

function formatDuration(row: TaskHistoryRow): string {
  if (!row.startedAt) return '-'
  const start = new Date(row.startedAt).getTime()
  const end = row.endedAt ? new Date(row.endedAt).getTime() : Date.now()
  const ms = Math.max(0, end - start)
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  return `${(ms / 60000).toFixed(1)}m`
}

export default function TaskHistoryPage() {
  const [rows, setRows] = useState<TaskHistoryRow[]>([])
  const [loading, setLoading] = useState(true)
  const navigate = useNavigate()
  const intervalRef = useRef<ReturnType<typeof setInterval>>(0 as unknown as ReturnType<typeof setInterval>)

  const load = async () => {
    try {
      const runs = await listRuns()
      const recentRuns = (Array.isArray(runs) ? runs : []).slice(0, 30)
      const details = await Promise.allSettled(recentRuns.map((r) => getRun(r.id)))

      const merged: TaskHistoryRow[] = []

      details.forEach((result, index) => {
        const run = recentRuns[index]
        if (!run || result.status !== 'fulfilled') return

        const steps = result.value.steps ?? []
        for (const step of steps) {
          merged.push({
            runId: run.id,
            pipelineName: run.pipeline_name,
            triggerType: run.scheduled_at ? 'schedule' : 'manual',
            stepName: step.step_name,
            status: step.status,
            startedAt: step.started_at,
            endedAt: step.ended_at,
            attempts: step.attempts,
            error: step.error,
          })
        }
      })

      merged.sort((a, b) => {
        const at = new Date(a.startedAt ?? 0).getTime()
        const bt = new Date(b.startedAt ?? 0).getTime()
        return bt - at
      })

      setRows(merged)
    } catch {
      // keep previous rows
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
    intervalRef.current = setInterval(load, 4000)
    return () => clearInterval(intervalRef.current)
  }, [])

  const columns: DataGridColumnDef<TaskHistoryRow>[] = useMemo(
    () => [
      {
        id: 'runId',
        header: 'Run',
        meta: { minWidth: 220 },
        cell: ({ row }) => (
          <button
            type="button"
            onClick={() => navigate(`/runs/${row.original.runId}`)}
            className="font-mono text-xs text-indigo-400 hover:text-indigo-300"
          >
            {row.original.runId}
          </button>
        ),
      },
      {
        accessorKey: 'pipelineName',
        header: 'Pipeline',
        meta: { minWidth: 180, flex: 1 },
      },
      {
        id: 'triggerType',
        header: 'Trigger',
        meta: { minWidth: 120 },
        cell: ({ row }) => (
          <span
            className={`rounded px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide ${row.original.triggerType === 'schedule' ? 'border border-emerald-700 bg-emerald-900/30 text-emerald-300' : 'border border-indigo-700 bg-indigo-900/30 text-indigo-300'}`}
          >
            {row.original.triggerType}
          </span>
        ),
      },
      {
        accessorKey: 'stepName',
        header: 'Task',
        meta: { minWidth: 160 },
      },
      {
        accessorKey: 'status',
        header: 'Status',
        meta: { minWidth: 120 },
        cell: ({ row }) => <StatusBadge status={row.original.status} />,
      },
      {
        id: 'startedAt',
        header: 'Started',
        meta: { minWidth: 190 },
        cell: ({ row }) => <span className="text-gray-400">{formatTime(row.original.startedAt)}</span>,
      },
      {
        id: 'duration',
        header: 'Duration',
        meta: { minWidth: 110, align: 'right' },
        cell: ({ row }) => <span className="text-gray-400">{formatDuration(row.original)}</span>,
      },
      {
        id: 'attempts',
        header: 'Try',
        meta: { minWidth: 70, align: 'right' },
        cell: ({ row }) => <span className="text-gray-400">{row.original.attempts}</span>,
      },
      {
        id: 'error',
        header: 'Error',
        meta: { minWidth: 220, flex: 1 },
        cell: ({ row }) => <span className="block truncate text-xs text-red-400">{row.original.error ?? '-'}</span>,
      },
    ],
    [navigate],
  )

  return (
    <div className="space-y-6">
      <section className="rounded-xl border border-gray-800 bg-gray-900 p-6">
        <h1 className="text-lg font-semibold text-gray-100">History</h1>
        <p className="mt-1 text-sm text-gray-400">Task execution history across manual, one-time, and cron-triggered runs.</p>
      </section>

      <section>
        {loading ? (
          <p className="text-sm text-gray-500">Loading...</p>
        ) : rows.length === 0 ? (
          <p className="text-sm text-gray-500">No task history yet.</p>
        ) : (
          <div className="overflow-hidden rounded-xl border border-gray-800 bg-gray-950">
            <DataGrid
              data={rows}
              columns={columns}
              tableHeight="auto"
              rowHeight={44}
              rowCursor
              onRowClick={(row) => navigate(`/runs/${row.runId}`)}
              pagination={{ pageSize: 15 }}
              footer={(table) => <DataGridPaginationBar table={table} pageSizes={[15, 30, 50]} />}
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
