import type { DataGridColumnDef } from '@loykin/gridkit'
import { Badge } from '@/components/ui/badge'
import type { User } from './types'

export const userColumns: DataGridColumnDef<User>[] = [
  {
    accessorKey: 'username',
    header: 'Username',
    meta: { minWidth: 180, flex: 1 },
    cell: ({ row }) => <span className="font-medium">{row.original.username}</span>,
  },
  {
    accessorKey: 'system_admin',
    header: 'Access',
    meta: { minWidth: 140 },
    cell: ({ row }) => (
      <Badge variant={row.original.system_admin ? 'default' : 'outline'}>
        {row.original.system_admin ? 'System admin' : 'User'}
      </Badge>
    ),
  },
  {
    accessorKey: 'disabled',
    header: 'Status',
    meta: { minWidth: 110 },
    cell: ({ row }) => (
      <Badge variant={row.original.disabled ? 'secondary' : 'outline'}>
        {row.original.disabled ? 'Disabled' : 'Active'}
      </Badge>
    ),
  },
]
