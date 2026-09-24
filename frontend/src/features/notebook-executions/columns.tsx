import type { DataGridColumnDef } from '@loykin/gridkit'
import { fmtDate } from '@/lib/format'
import StatusBadge from '@/shared/components/StatusBadge'
import type { NotebookExecution } from './types'

/** Columns for the project's notebook execution list; `actorNames` resolves requester ids to usernames. */
export function getExecutionColumns(actorNames: Map<string, string>): DataGridColumnDef<NotebookExecution>[] {
  return [
    { accessorKey: 'notebook_name', header: 'Notebook' },
    { accessorKey: 'notebook_path', header: 'Path' },
    { accessorKey: 'status', header: 'Status', cell: ({ row }) => <StatusBadge status={row.original.status} /> },
    { id: 'progress', header: 'Progress', cell: ({ row }) => `${row.original.current_cell} / ${row.original.total_cells}` },
    { accessorKey: 'requested_by', header: 'Requested by', cell: ({ row }) => row.original.requested_by_username || (row.original.requested_by ? actorNames.get(row.original.requested_by) ?? row.original.requested_by : '—') },
    { accessorKey: 'queued_at', header: 'Queued', cell: ({ row }) => fmtDate(row.original.queued_at) },
  ]
}
