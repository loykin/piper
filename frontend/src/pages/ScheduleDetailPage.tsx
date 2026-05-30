import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { getSchedule, listScheduleRuns, setScheduleEnabled, deleteSchedule, type Schedule, type Run } from '../api'
import { DataGrid, DataGridPaginationCompact, type DataGridColumnDef } from '@loykin/gridkit'
import { DataPage } from '@loykin/designkit'
import { Badge } from '@/components/ui/badge'
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
        <Link to={`/runs/${row.original.id}`} className="font-mono text-xs text-primary hover:underline">
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
      cell: ({ row }) => <span className="text-xs text-muted-foreground">{new Date(row.original.started_at).toLocaleString()}</span>,
    },
    {
      id: 'ended_at',
      header: 'Ended',
      meta: { minWidth: 180 },
      cell: ({ row }) => (
        <span className="text-xs text-muted-foreground">
          {row.original.ended_at ? new Date(row.original.ended_at).toLocaleString() : '-'}
        </span>
      ),
    },
  ]

  if (loading) {
    return (
      <DataPage>
        <DataPage.Content>
          <p className="text-sm text-muted-foreground">Loading...</p>
        </DataPage.Content>
      </DataPage>
    )
  }

  if (!schedule) {
    return (
      <DataPage>
        <DataPage.Content>
          <p className="text-sm text-muted-foreground">Schedule not found.</p>
        </DataPage.Content>
      </DataPage>
    )
  }

  const isCron = schedule.schedule_type === 'cron'

  return (
    <DataPage>
      <DataPage.Header>
        <DataPage.TitleBlock
          breadcrumb={<Link to="/schedules" className="hover:text-foreground transition-colors">← Schedules</Link>}
          title={schedule.name}
          description={`${TYPE_LABEL[schedule.schedule_type] ?? schedule.schedule_type} schedule`}
        />
        <DataPage.Actions>
          {isCron && (
            <button
              type="button"
              onClick={async () => {
                try { await setScheduleEnabled(schedule.id, !schedule.enabled); await load() } catch { /* no-op */ }
              }}
              className={`rounded border px-3 py-1.5 text-xs font-medium transition-colors ${
                schedule.enabled
                  ? 'border-primary/40 bg-primary/10 text-primary'
                  : 'border-border bg-secondary text-muted-foreground'
              }`}
            >
              {schedule.enabled ? 'Enabled' : 'Disabled'}
            </button>
          )}
          <Badge variant="outline">{TYPE_LABEL[schedule.schedule_type] ?? schedule.schedule_type}</Badge>
          <button
            type="button"
            onClick={async () => {
              if (!confirm(`Delete schedule "${schedule.name}"?`)) return
              try { await deleteSchedule(schedule.id); navigate('/schedules') } catch { /* no-op */ }
            }}
            className="rounded border border-destructive/40 px-3 py-1.5 text-xs font-medium text-destructive hover:bg-destructive/10"
          >
            Delete
          </button>
        </DataPage.Actions>
      </DataPage.Header>

      <DataPage.Content>
        {/* Meta info */}
        <DataPage.Group surface="bordered" className="mb-4">
          <div className="p-4">
            <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
              {isCron && (
                <div>
                  <dt className="text-xs text-muted-foreground">Cron Expression</dt>
                  <dd className="mt-1 font-mono text-sm">{schedule.cron_expr || '-'}</dd>
                </div>
              )}
              {schedule.schedule_type === 'once' && (
                <div>
                  <dt className="text-xs text-muted-foreground">Scheduled At</dt>
                  <dd className="mt-1 text-sm">{new Date(schedule.next_run_at).toLocaleString()}</dd>
                </div>
              )}
              <div>
                <dt className="text-xs text-muted-foreground">Status</dt>
                <dd className="mt-1 text-sm">
                  {schedule.enabled ? (isCron ? 'Active' : 'Waiting') : (schedule.schedule_type === 'cron' ? 'Disabled' : 'Done')}
                </dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">Last Run</dt>
                <dd className="mt-1 text-sm">
                  {schedule.last_run_at ? new Date(schedule.last_run_at).toLocaleString() : '-'}
                </dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">Created</dt>
                <dd className="mt-1 text-sm">{new Date(schedule.created_at).toLocaleString()}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">Total Runs</dt>
                <dd className="mt-1 text-sm font-semibold">{runs.length}</dd>
              </div>
            </dl>
          </div>
        </DataPage.Group>

        {/* Pipeline DAG */}
        <DataPage.Group surface="bordered" className="mb-4">
          <RunDAG
            pipelineYaml={schedule.pipeline_yaml}
            steps={[]}
            selected={selected}
            onSelectStep={() => {}}
          />
        </DataPage.Group>

        {/* Run History */}
        <DataPage.Group surface="none" className="mb-4">
          <DataPage.GroupHeader
            title="Run History"
            className="px-4 pt-3"
          />
          {runs.length === 0 ? (
            <div className="px-4 py-8 text-sm text-muted-foreground">
              {schedule.schedule_type === 'immediate' ? 'Running...' : 'No runs yet.'}
            </div>
          ) : (
            <DataPage.GroupBody className="[&_.dg-shell]:h-full [&_.dg-table-wrapper]:min-h-0 [&_.dg-table-wrapper]:flex-1">
              <DataGrid
                data={runs}
                columns={runColumns}
                tableWidthMode="fill-last"
                rowHeight={44}
                pagination={{ pageSize: 10 }}
                footer={(table) => (
                  <div className="flex h-9 items-center justify-between px-1 text-xs text-muted-foreground">
                    <span>{runs.length} results</span>
                    <DataGridPaginationCompact table={table} />
                  </div>
                )}
              />
            </DataPage.GroupBody>
          )}
        </DataPage.Group>

        {/* YAML */}
        <DataPage.Group surface="bordered">
          <DataPage.GroupHeader title="Pipeline YAML" className="px-4 pt-3" />
          <pre className="overflow-x-auto px-4 pb-4 text-xs leading-6 text-muted-foreground">{schedule.pipeline_yaml || '(empty)'}</pre>
        </DataPage.Group>
      </DataPage.Content>
    </DataPage>
  )
}
