import { Link } from 'react-router-dom'
import type { DataGridColumnDef } from '@loykin/gridkit'
import StatusBadge from '@/shared/components/StatusBadge'
import type { Service, ServiceHistory } from './api'

export const serviceColumns: DataGridColumnDef<Service>[] = [
  {
    accessorKey: 'name',
    header: 'Name',
    meta: { minWidth: 180 },
    cell: ({ row }) => (
      <Link to={`/serving/${row.original.name}`} className="font-medium text-primary hover:underline">
        {row.original.name}
      </Link>
    ),
  },
  {
    accessorKey: 'status',
    header: 'Status',
    meta: { minWidth: 110 },
    cell: ({ row }) => <StatusBadge status={row.original.status} />,
  },
  {
    accessorKey: 'artifact',
    header: 'Artifact',
    meta: { minWidth: 180, flex: 1 },
    cell: ({ row }) => <span className="font-mono text-xs text-muted-foreground">{row.original.artifact || '—'}</span>,
  },
  {
    id: 'namespace',
    header: 'Namespace',
    meta: { minWidth: 110 },
    cell: ({ row }) => (
      <span className="text-xs text-muted-foreground">{row.original.namespace || 'local'}</span>
    ),
  },
  {
    accessorKey: 'endpoint',
    header: 'Endpoint',
    meta: { minWidth: 200 },
    cell: ({ row }) => {
      const ep = row.original.endpoint
      return ep ? (
        <a href={ep} target="_blank" rel="noreferrer" className="font-mono text-xs text-primary hover:underline">{ep}</a>
      ) : <span className="text-xs text-muted-foreground">—</span>
    },
  },
  {
    accessorKey: 'updated_at',
    header: 'Updated',
    meta: { minWidth: 160 },
    cell: ({ row }) => <span className="text-xs text-muted-foreground">{new Date(row.original.updated_at).toLocaleString()}</span>,
  },
]

function elapsed(deployedAt: string, stoppedAt: string): string {
  const ms = new Date(stoppedAt).getTime() - new Date(deployedAt).getTime()
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  if (ms < 3_600_000) return `${(ms / 60000).toFixed(1)}m`
  return `${(ms / 3_600_000).toFixed(1)}h`
}

export const serviceHistoryColumns: DataGridColumnDef<ServiceHistory>[] = [
  {
    accessorKey: 'name',
    header: 'Service',
    meta: { minWidth: 160 },
    cell: ({ row }) => <span className="font-medium">{row.original.name}</span>,
  },
  {
    accessorKey: 'status',
    header: 'Final Status',
    meta: { minWidth: 120 },
    cell: ({ row }) => <StatusBadge status={row.original.status} />,
  },
  {
    accessorKey: 'artifact',
    header: 'Artifact',
    meta: { minWidth: 180, flex: 1 },
    cell: ({ row }) => <span className="font-mono text-xs text-muted-foreground">{row.original.artifact || '—'}</span>,
  },
  {
    id: 'run_id',
    header: 'Source Run',
    meta: { minWidth: 200 },
    cell: ({ row }) => row.original.run_id ? (
      <Link to={`/runs/${row.original.run_id}`} className="font-mono text-xs text-primary hover:underline">
        {row.original.run_id}
      </Link>
    ) : <span className="text-xs text-muted-foreground">—</span>,
  },
  {
    id: 'namespace',
    header: 'Namespace',
    meta: { minWidth: 110 },
    cell: ({ row }) => <span className="text-xs text-muted-foreground">{row.original.namespace || 'local'}</span>,
  },
  {
    id: 'deployed_at',
    header: 'Deployed',
    meta: { minWidth: 160 },
    cell: ({ row }) => <span className="text-xs text-muted-foreground">{new Date(row.original.deployed_at).toLocaleString()}</span>,
  },
  {
    id: 'stopped_at',
    header: 'Stopped',
    meta: { minWidth: 160 },
    cell: ({ row }) => <span className="text-xs text-muted-foreground">{new Date(row.original.stopped_at).toLocaleString()}</span>,
  },
  {
    id: 'duration',
    header: 'Duration',
    meta: { minWidth: 100 },
    cell: ({ row }) => <span className="text-xs text-muted-foreground">{elapsed(row.original.deployed_at, row.original.stopped_at)}</span>,
  },
]
