import type { DataGridColumnDef } from '@loykin/gridkit'
import { Badge } from '@/components/ui/badge'
import type { Connection } from './types'

function fmtDate(value?: string): string {
  if (!value || value.startsWith('0001-01-01')) return '-'
  const ts = new Date(value)
  if (Number.isNaN(ts.getTime())) return '-'
  return ts.toLocaleString()
}

export const connectionColumns: DataGridColumnDef<Connection>[] = [
  {
    accessorKey: 'name',
    header: 'Name',
    meta: { minWidth: 200, flex: 1 },
    cell: ({ row }) => <span className="font-medium">{row.original.name}</span>,
  },
  {
    accessorKey: 'type',
    header: 'Type',
    meta: { minWidth: 110 },
    cell: ({ row }) => <Badge variant="outline">{row.original.type}</Badge>,
  },
  {
    accessorKey: 'endpoint',
    header: 'Endpoint',
    meta: { minWidth: 220, flex: 1 },
    cell: ({ row }) => (
      <span className="font-mono text-xs text-muted-foreground">
        {row.original.endpoint || <em className="not-italic text-muted-foreground/60">any</em>}
      </span>
    ),
  },
  {
    id: 'status',
    header: 'Status',
    meta: { minWidth: 100 },
    cell: ({ row }) => {
      const { disabled, last_test_ok } = row.original
      if (disabled) return <Badge variant="secondary">Disabled</Badge>
      if (last_test_ok === true) return <Badge variant="default">Verified</Badge>
      if (last_test_ok === false) return <Badge variant="destructive">Failed</Badge>
      return <Badge variant="outline">Active</Badge>
    },
  },
  {
    accessorKey: 'last_used_at',
    header: 'Last Used',
    meta: { minWidth: 150 },
    cell: ({ row }) => <span className="text-xs text-muted-foreground">{fmtDate(row.original.last_used_at)}</span>,
  },
  {
    accessorKey: 'updated_at',
    header: 'Updated',
    meta: { minWidth: 150 },
    cell: ({ row }) => <span className="text-xs text-muted-foreground">{fmtDate(row.original.updated_at)}</span>,
  },
]
