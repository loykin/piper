import type { DataGridColumnDef } from '@loykin/gridkit'
import type { Schedule } from './api'

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
            ? new Date(row.original.next_run_at).toLocaleString()
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
            ? new Date(row.original.next_run_at).toLocaleString()
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
        {row.original.last_run_at ? new Date(row.original.last_run_at).toLocaleString() : '—'}
      </span>
    ),
  },
]
