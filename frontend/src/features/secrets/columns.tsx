import type { DataGridColumnDef } from '@loykin/gridkit'
import { Badge } from '@/components/ui/badge'
import type { SecretMetadata } from './types'

function fmtDate(value?: string): string {
  if (!value || value.startsWith('0001-01-01')) return '-'
  const ts = new Date(value)
  if (Number.isNaN(ts.getTime())) return '-'
  return ts.toLocaleString()
}

export const secretColumns: DataGridColumnDef<SecretMetadata>[] = [
  {
    accessorKey: 'name',
    header: 'Name',
    meta: { minWidth: 220, flex: 1 },
    cell: ({ row }) => <span className="font-medium">{row.original.name}</span>,
  },
  {
    accessorKey: 'provider',
    header: 'Provider',
    meta: { minWidth: 150 },
    cell: ({ row }) => <Badge variant="outline">{row.original.provider}</Badge>,
  },
  {
    id: 'keys',
    header: 'Keys',
    meta: { minWidth: 180 },
    cell: ({ row }) => <span className="font-mono text-xs text-muted-foreground">{row.original.keys.join(', ') || '-'}</span>,
  },
  {
    id: 'status',
    header: 'Status',
    meta: { minWidth: 100 },
    cell: ({ row }) => (
      <Badge variant={row.original.disabled ? 'secondary' : 'default'}>
        {row.original.disabled ? 'Disabled' : 'Active'}
      </Badge>
    ),
  },
  {
    accessorKey: 'last_used_at',
    header: 'Last Used',
    meta: { minWidth: 160 },
    cell: ({ row }) => <span className="text-xs text-muted-foreground">{fmtDate(row.original.last_used_at)}</span>,
  },
  {
    accessorKey: 'updated_at',
    header: 'Updated',
    meta: { minWidth: 160 },
    cell: ({ row }) => <span className="text-xs text-muted-foreground">{fmtDate(row.original.updated_at)}</span>,
  },
]

