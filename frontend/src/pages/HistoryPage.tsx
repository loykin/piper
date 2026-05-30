import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { DataGrid, DataGridPaginationCompact, type DataGridColumnDef } from '@loykin/gridkit'
import { DataPage } from '@loykin/designkit'
import { listRuns, deleteRun, rerunRun, type Run, type Step } from '../api'
import StatusBadge from '../components/StatusBadge'

function elapsed(startedAt: string, endedAt?: string): string {
  const ms = (endedAt ? new Date(endedAt) : new Date()).getTime() - new Date(startedAt).getTime()
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  return `${(ms / 60000).toFixed(1)}m`
}

const STEP_COLOR: Record<string, string> = {
  done:     'bg-green-500',
  success:  'bg-green-500',
  running:  'bg-blue-400 animate-pulse',
  failed:   'bg-red-500',
  skipped:  'bg-yellow-600',
  canceled: 'bg-orange-500',
  pending:  'bg-gray-600',
}

function StepDots({ steps }: { steps: Step[] }) {
  if (!steps.length) return <span className="text-xs text-muted-foreground">—</span>
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

export default function HistoryPage() {
  const [runs, setRuns] = useState<Run[]>([])
  const [loading, setLoading] = useState(true)
  const [deleting, setDeleting] = useState<string | null>(null)
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

  const handleDelete = (e: React.MouseEvent, run: Run) => {
    e.stopPropagation()
    if (!confirm(`Delete run ${run.id}?\nArtifacts will also be removed.`)) return
    setDeleting(run.id)
    deleteRun(run.id).then(load).catch((err) => alert(err.message)).finally(() => setDeleting(null))
  }

  const handleRerun = (e: React.MouseEvent, run: Run, failedOnly = false) => {
    e.stopPropagation()
    rerunRun(run.id, failedOnly)
      .then(({ run_id }) => navigate(`/runs/${run_id}`))
      .catch((err) => alert(err.message))
  }

  const columns: DataGridColumnDef<Run>[] = [
    {
      accessorKey: 'id',
      header: 'Run ID',
      meta: { minWidth: 240 },
      cell: ({ row }) => <span className="font-mono text-xs text-primary">{row.original.id}</span>,
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
      meta: { minWidth: 180 },
      cell: ({ row }) => <span className="text-xs text-muted-foreground">{new Date(row.original.started_at).toLocaleString()}</span>,
    },
    {
      id: 'duration',
      header: 'Duration',
      meta: { minWidth: 100 },
      cell: ({ row }) => <span className="text-xs text-muted-foreground">{elapsed(row.original.started_at, row.original.ended_at)}</span>,
    },
    {
      id: 'actions',
      header: '',
      meta: { minWidth: 210, align: 'right' },
      cell: ({ row }) => (
        <div className="flex justify-end gap-1">
          <button type="button"
            disabled={row.original.status === 'running' || row.original.status === 'scheduled'}
            onClick={(e) => handleRerun(e, row.original)}
            className="rounded px-2 py-1 text-xs text-primary hover:bg-primary/10 disabled:cursor-not-allowed disabled:opacity-30">
            Rerun
          </button>
          <button type="button"
            disabled={row.original.status !== 'failed'}
            onClick={(e) => handleRerun(e, row.original, true)}
            className="rounded px-2 py-1 text-xs text-yellow-400 hover:bg-yellow-400/10 disabled:cursor-not-allowed disabled:opacity-30">
            Failed
          </button>
          <button type="button"
            disabled={row.original.status === 'running' || deleting === row.original.id}
            onClick={(e) => handleDelete(e, row.original)}
            className="rounded px-2 py-1 text-xs text-destructive hover:bg-destructive/10 disabled:cursor-not-allowed disabled:opacity-30">
            {deleting === row.original.id ? '…' : 'Delete'}
          </button>
        </div>
      ),
    },
  ]

  return (
    <DataPage>
      <DataPage.Header>
        <DataPage.TitleBlock
          title="History"
          description="All pipeline run records. Each square in Steps represents one step's status."
        />
      </DataPage.Header>
      <DataPage.Content>
        {loading ? (
          <div className="py-8 text-sm text-muted-foreground">Loading…</div>
        ) : runs.length === 0 ? (
          <div className="py-8 text-sm text-muted-foreground">No runs yet.</div>
        ) : (
          <DataPage.Group surface="none" className="h-full">
            <DataPage.GroupBody className="h-full [&_.dg-shell]:h-full [&_.dg-table-wrapper]:min-h-0 [&_.dg-table-wrapper]:flex-1">
              <DataGrid
                data={runs}
                columns={columns}
                tableWidthMode="fill-last"
                rowHeight={44}
                rowCursor
                onRowClick={(row) => navigate(`/runs/${row.id}`)}
                pagination={{ pageSize: 20 }}
                footer={(table) => (
                  <div className="flex h-9 items-center justify-between px-1 text-xs text-muted-foreground">
                    <span>{runs.length} results</span>
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
