import { Link, useNavigate, useParams } from 'react-router-dom'
import { useProjectId } from '@/lib/projectContext'
import { Power, Trash2 } from 'lucide-react'
import { DataGrid, DataGridPaginationCompact, type DataGridColumnDef } from '@loykin/gridkit'
import { DetailBodyTemplate } from '@loykin/designkit'
import { IconButton } from '@/components/ui/icon-button'
import { Badge } from '@/components/ui/badge'
import RunDAG from '@/shared/components/RunDAG'
import StatusBadge from '@/shared/components/StatusBadge'
import { useSchedule, useScheduleRuns, useDeleteSchedule, useToggleSchedule } from '@/features/schedules/hooks'
import type { Run } from '@/features/runs/api'

const TYPE_LABEL: Record<string, string> = {
  immediate: 'Immediate',
  once: 'Once',
  cron: 'Cron',
}

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

export default function ScheduleDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const projectId = useProjectId()
  const { data: schedule, isLoading: scheduleLoading } = useSchedule(id!)
  const { data: runs = [], isLoading: runsLoading } = useScheduleRuns(id!)
  const { mutate: deleteSchedule } = useDeleteSchedule()
  const { mutate: toggleSchedule } = useToggleSchedule()

  const loading = scheduleLoading || runsLoading

  if (loading) {
    return (
      <DetailBodyTemplate title="Loading…">
        <DetailBodyTemplate.Section>
          <p className="text-sm text-muted-foreground">Loading…</p>
        </DetailBodyTemplate.Section>
      </DetailBodyTemplate>
    )
  }

  if (!schedule) {
    return (
      <DetailBodyTemplate title="Not Found">
        <DetailBodyTemplate.Section>
          <p className="text-sm text-muted-foreground">Schedule not found.</p>
        </DetailBodyTemplate.Section>
      </DetailBodyTemplate>
    )
  }

  const isCron = schedule.schedule_type === 'cron'

  return (
    <DetailBodyTemplate
      eyebrow={<Link to={`/projects/${projectId}/schedules`} className="hover:text-foreground transition-colors">← Schedules</Link>}
      title={schedule.name}
      description={`${TYPE_LABEL[schedule.schedule_type] ?? schedule.schedule_type} schedule`}
      actions={
        <div className="flex items-center gap-0.5">
          {isCron && (
            <IconButton icon={<Power />} label={schedule.enabled ? 'Disable' : 'Enable'}
              onClick={() => toggleSchedule({ id: schedule.id, enabled: !schedule.enabled })}
              className={schedule.enabled ? 'text-primary hover:bg-primary/10' : ''} />
          )}
          <Badge variant="outline">{TYPE_LABEL[schedule.schedule_type] ?? schedule.schedule_type}</Badge>
          <IconButton icon={<Trash2 />} label="Delete"
            onClick={() => {
              if (!confirm(`Delete schedule "${schedule.name}"?`)) return
              deleteSchedule(schedule.id, { onSuccess: () => navigate(`/projects/${projectId}/schedules`) })
            }}
            className="text-destructive hover:bg-destructive/10" />
        </div>
      }
    >
      <DetailBodyTemplate.Section title="Details" surface="bordered">
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
      </DetailBodyTemplate.Section>

      <DetailBodyTemplate.Section surface="bordered">
        <RunDAG
          pipelineYaml={schedule.pipeline_yaml}
          steps={[]}
          selected={null}
          onSelectStep={() => {}}
        />
      </DetailBodyTemplate.Section>

      <DetailBodyTemplate.Section title="Run History" surface="plain">
        {runs.length === 0 ? (
          <p className="py-4 text-sm text-muted-foreground">
            {schedule.schedule_type === 'immediate' ? 'Running...' : 'No runs yet.'}
          </p>
        ) : (
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
        )}
      </DetailBodyTemplate.Section>

      <DetailBodyTemplate.Section title="Pipeline YAML" surface="bordered">
        <pre className="overflow-x-auto text-xs leading-6 text-muted-foreground">{schedule.pipeline_yaml || '(empty)'}</pre>
      </DetailBodyTemplate.Section>
    </DetailBodyTemplate>
  )
}
