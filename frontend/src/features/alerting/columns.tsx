import type { DataGridColumnDef } from '@loykin/gridkit'
import { Badge } from '@/components/ui/badge'
import type { AlertRule } from './types'

const date = (value?: string) => value ? new Date(value).toLocaleString() : '—'
export const alertRuleColumns: DataGridColumnDef<AlertRule>[] = [
  { accessorKey: 'name', header: 'Name', meta: { minWidth: 180, flex: 1 }, cell: ({ row }) => <span className="font-medium">{row.original.name}</span> },
  { accessorKey: 'on', header: 'Source', meta: { minWidth: 90 }, cell: ({ row }) => <Badge variant="outline">{row.original.on}</Badge> },
  { id: 'condition', header: 'Condition', meta: { minWidth: 230, flex: 1 }, cell: ({ row }) => <span className="font-mono text-xs text-muted-foreground">{row.original.on === 'event' ? `${row.original.event_type}${row.original.when ? ` · ${row.original.when}` : ''}` : `${row.original.metric_key} ${row.original.condition}`}</span> },
  { id: 'notify', header: 'Notify', meta: { minWidth: 180 }, cell: ({ row }) => <div className="flex flex-wrap gap-1">{row.original.notify.map(ref => <Badge key={ref} variant="secondary">{ref}</Badge>)}</div> },
  { accessorKey: 'cooldown_seconds', header: 'Cooldown', meta: { minWidth: 100 }, cell: ({ row }) => <span className="text-xs">{row.original.cooldown_seconds}s</span> },
  { accessorKey: 'last_success_at', header: 'Last Success', meta: { minWidth: 160 }, cell: ({ row }) => <span className="text-xs text-muted-foreground">{date(row.original.last_success_at)}</span> },
]
