import type { DataGridColumnDef } from '@loykin/gridkit'
import { Badge } from '@/components/ui/badge'
import type { ProjectMember } from './types'

export const memberColumns: DataGridColumnDef<ProjectMember>[] = [
  {
    accessorKey: 'username',
    header: 'Username',
    meta: { minWidth: 180, flex: 1 },
    cell: ({ row }) => (
      <span className="font-medium">{row.original.username || 'Unknown user'}</span>
    ),
  },
  {
    accessorKey: 'role',
    header: 'Project role',
    meta: { minWidth: 140 },
    cell: ({ row }) => <Badge variant="outline">{row.original.role}</Badge>,
  },
]
