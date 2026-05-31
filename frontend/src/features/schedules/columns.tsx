import type { DataGridColumnDef } from '@loykin/gridkit'
import { Badge } from '@/components/ui/badge'
import type { Schedule } from './api'

export const scheduleColumns: DataGridColumnDef<Schedule>[] = [
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
      <Badge variant={row.original.schedule_type === 'cron' ? 'default' : 'secondary'}>
        {row.original.schedule_type === 'cron' ? 'Cron' : 'Once'}
      </Badge>
    ),
  },
  {
    id: 'schedule',
    header: 'Schedule',
    meta: { minWidth: 200 },
    cell: ({ row }) => (
      <span className="font-mono text-xs text-muted-foreground">
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
      <span className="text-xs text-muted-foreground">
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
      <span className="text-xs text-muted-foreground">
        {row.original.last_run_at ? new Date(row.original.last_run_at).toLocaleString() : '-'}
      </span>
    ),
  },
  {
    id: 'created_at',
    header: 'Created',
    meta: { minWidth: 180 },
    cell: ({ row }) => (
      <span className="text-xs text-muted-foreground">{new Date(row.original.created_at).toLocaleString()}</span>
    ),
  },
]
