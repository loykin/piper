import type { DataGridColumnDef } from '@loykin/gridkit'
import { Badge } from '@/components/ui/badge'
import type { Worker } from './api'
import type { NotebookWorkerInfo } from '@/features/notebooks/api'
import type { ServingWorkerInfo } from '@/features/serving/api'

function relativeTime(ts: string): string {
  const ms = Date.now() - new Date(ts).getTime()
  if (ms < 60_000) return `${Math.floor(ms / 1000)}s ago`
  if (ms < 3_600_000) return `${Math.floor(ms / 60_000)}m ago`
  return `${Math.floor(ms / 3_600_000)}h ago`
}

function isOnline(w: Worker): boolean {
  return w.status === 'online' && Date.now() - new Date(w.last_seen).getTime() < 30_000
}

function isNodeOnline(last_seen: string): boolean {
  return Date.now() - new Date(last_seen).getTime() < 30_000
}

// ── Pipeline Workers ──────────────────────────────────────────────────────────

export const pipelineColumns: DataGridColumnDef<Worker>[] = [
  {
    id: 'status',
    header: 'Status',
    meta: { minWidth: 90 },
    cell: ({ row }) => (
      <Badge variant={isOnline(row.original) ? 'default' : 'secondary'}>
        {isOnline(row.original) ? 'Online' : 'Offline'}
      </Badge>
    ),
  },
  {
    accessorKey: 'label',
    header: 'Label',
    meta: { minWidth: 160 },
    cell: ({ row }) => <span className="font-medium">{row.original.label || '—'}</span>,
  },
  {
    accessorKey: 'hostname',
    header: 'Hostname',
    size: 180,
    meta: { minWidth: 180 },
    cell: ({ row }) => <span className="text-muted-foreground">{row.original.hostname}</span>,
  },
  {
    id: 'load',
    header: 'Load',
    size: 90,
    meta: { minWidth: 90, align: 'right' },
    cell: ({ row }) => (
      <span className="font-mono text-xs text-muted-foreground">
        {row.original.in_flight}&thinsp;/&thinsp;{row.original.concurrency}
      </span>
    ),
  },
  {
    accessorKey: 'capabilities',
    header: 'Capabilities',
    meta: { minWidth: 160, flex: 1 },
    cell: ({ row }) => (
      <span className="text-xs text-muted-foreground">{row.original.capabilities || '—'}</span>
    ),
  },
  {
    accessorKey: 'version',
    header: 'Version',
    meta: { minWidth: 100 },
    cell: ({ row }) => (
      <span className="font-mono text-xs text-muted-foreground">{row.original.version || '—'}</span>
    ),
  },
  {
    id: 'last_seen',
    header: 'Last Seen',
    size: 110,
    meta: { minWidth: 110 },
    cell: ({ row }) => (
      <span className="text-xs text-muted-foreground">{relativeTime(row.original.last_seen)}</span>
    ),
  },
]

// ── Notebook / Serving Worker node columns (shared shape) ─────────────────────

export type NodeInfo = NotebookWorkerInfo | ServingWorkerInfo

export const nodeColumns: DataGridColumnDef<NodeInfo>[] = [
  {
    id: 'status',
    header: 'Status',
    size: 90,
    meta: { minWidth: 90 },
    cell: ({ row }) => (
      <Badge variant={isNodeOnline(row.original.last_seen) ? 'default' : 'secondary'}>
        {isNodeOnline(row.original.last_seen) ? 'Online' : 'Offline'}
      </Badge>
    ),
  },
  {
    accessorKey: 'hostname',
    header: 'Hostname',
    size: 180,
    meta: { minWidth: 180 },
    cell: ({ row }) => <span className="font-medium">{row.original.hostname}</span>,
  },
  {
    accessorKey: 'addr',
    header: 'Address',
    size: 200,
    meta: { minWidth: 150 },
    cell: ({ row }) => (
      <span className="font-mono text-xs text-muted-foreground">{row.original.addr}</span>
    ),
  },
  {
    id: 'gpus',
    header: 'GPUs',
    meta: { minWidth: 160, flex: 1 },
    cell: ({ row }) => (
      <span className="text-xs text-muted-foreground">
        {row.original.gpus?.length ? row.original.gpus.join(', ') : '—'}
      </span>
    ),
  },
  {
    id: 'last_seen',
    header: 'Last Seen',
    size: 110,
    meta: { minWidth: 110 },
    cell: ({ row }) => (
      <span className="text-xs text-muted-foreground">{relativeTime(row.original.last_seen)}</span>
    ),
  },
]
