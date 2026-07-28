import type { DataGridColumnDef } from '@loykin/gridkit'
import { Badge } from '@/components/ui/badge'
import type { Worker } from './api'

function relativeTime(ts: string): string {
  const ms = Date.now() - new Date(ts).getTime()
  if (ms < 60_000) return `${Math.floor(ms / 1000)}s ago`
  if (ms < 3_600_000) return `${Math.floor(ms / 60_000)}m ago`
  return `${Math.floor(ms / 3_600_000)}h ago`
}

export const workerColumns: DataGridColumnDef<Worker>[] = [
  {
    id: 'status',
    header: 'Status',
    meta: { minWidth: 90 },
    cell: () => (
      <Badge variant="default">Online</Badge>
    ),
  },
  {
    accessorKey: 'infrastructure',
    header: 'Infrastructure',
    meta: { minWidth: 160 },
    cell: ({ row }) => <span className="font-medium">{row.original.infrastructure}</span>,
  },
  {
    id: 'identity',
    header: 'Worker',
    size: 180,
    meta: { minWidth: 180 },
    cell: ({ row }) => (
      <span className="font-mono text-xs text-muted-foreground">
        {row.original.hostname || row.original.cluster_name || row.original.id}
      </span>
    ),
  },
  {
    id: 'load',
    header: 'Load',
    size: 90,
    meta: { minWidth: 90, align: 'right' },
    cell: ({ row }) => (
      <span className="font-mono text-xs text-muted-foreground">
        {row.original.capacity ?? '—'}
      </span>
    ),
  },
  {
    accessorKey: 'capabilities',
    header: 'Capabilities',
    meta: { minWidth: 160, flex: 1 },
    cell: ({ row }) => (
      <span className="text-xs text-muted-foreground">{row.original.capabilities.join(', ') || '—'}</span>
    ),
  },
  {
    accessorKey: 'cluster_name',
    header: 'Cluster',
    meta: { minWidth: 100 },
    cell: ({ row }) => (
      <span className="font-mono text-xs text-muted-foreground">{row.original.cluster_name || '—'}</span>
    ),
  },
  {
    id: 'registered_at',
    header: 'Connected',
    size: 110,
    meta: { minWidth: 110 },
    cell: ({ row }) => (
      <span className="text-xs text-muted-foreground">{relativeTime(row.original.registered_at)}</span>
    ),
  },
]
