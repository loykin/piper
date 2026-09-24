import type { DataGridColumnDef } from '@loykin/gridkit'
import { Link } from '@/lib/router'
import StatusBadge from '@/shared/components/StatusBadge'
import type { Schedule } from './api'
import type { Run } from '@/features/runs/types'
import { fmtDate } from '@/lib/format'

export const scheduleColumns: DataGridColumnDef<Schedule>[] = [
  // name column is overridden in WorkflowsPage to include version
  {
    accessorKey: 'name',
    header: 'Name',
    meta: { flex: 1, minWidth: 160 },
  },
  {
    id: 'schedule',
    header: 'Cron',
    meta: { minWidth: 120 },
    cell: ({ row }) => (
      <span className="font-mono text-xs text-muted-foreground">
        {row.original.schedule_type === 'cron'
          ? row.original.cron_expr || '—'
          : row.original.schedule_type === 'once'
            ? fmtDate(row.original.next_run_at)
            : 'Immediate'}
      </span>
    ),
  },
  {
    id: 'next_run_at',
    header: 'Next Run',
    meta: { minWidth: 130 },
    cell: ({ row }) => (
      <span className="text-xs text-muted-foreground">
        {row.original.schedule_type === 'once' && !row.original.enabled
          ? 'Done'
          : row.original.next_run_at
            ? fmtDate(row.original.next_run_at)
            : '—'}
      </span>
    ),
  },
  {
    id: 'max_runs',
    header: 'Retention',
    meta: { minWidth: 110 },
    cell: ({ row }) => (
      <span className="text-xs text-muted-foreground">
        {row.original.max_runs > 0 ? `${row.original.max_runs} runs` : 'All runs'}
      </span>
    ),
  },
  {
    id: 'last_run_at',
    header: 'Last Run',
    meta: { minWidth: 130 },
    cell: ({ row }) => (
      <span className="text-xs text-muted-foreground">
        {row.original.last_run_at ? fmtDate(row.original.last_run_at) : '—'}
      </span>
    ),
  },
]

/** Runs created by one schedule, linking each to its run detail page. */
export function makeScheduleRunColumns(projectId: string): DataGridColumnDef<Run>[] {
  return [
    {
      id: 'id',
      header: 'Run ID',
      meta: { minWidth: 200, flex: 1 },
      cell: ({ row }) => (
        <Link to={`/projects/${projectId}/runs/${row.original.id}`} className="font-mono text-xs text-primary hover:underline">
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
      cell: ({ row }) => <span className="text-xs text-muted-foreground">{fmtDate(row.original.started_at)}</span>,
    },
    {
      id: 'ended_at',
      header: 'Ended',
      meta: { minWidth: 180 },
      cell: ({ row }) => (
        <span className="text-xs text-muted-foreground">
          {row.original.ended_at ? fmtDate(row.original.ended_at) : '-'}
        </span>
      ),
    },
  ]
}
