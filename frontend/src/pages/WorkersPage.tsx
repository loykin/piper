import { useEffect, useRef, useState } from 'react'
import { DataGrid, DataGridPaginationCompact, type DataGridColumnDef } from '@loykin/gridkit'
import { DataPage } from '@loykin/designkit'
import { Badge } from '@/components/ui/badge'
import { listWorkers, type Worker } from '../api'

function relativeTime(ts: string): string {
  const ms = Date.now() - new Date(ts).getTime()
  if (ms < 60_000) return `${Math.floor(ms / 1000)}s ago`
  if (ms < 3_600_000) return `${Math.floor(ms / 60_000)}m ago`
  return `${Math.floor(ms / 3_600_000)}h ago`
}

function isOnline(w: Worker): boolean {
  return w.status === 'online' && Date.now() - new Date(w.last_seen).getTime() < 30_000
}

const columns: DataGridColumnDef<Worker>[] = [
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
    meta: { minWidth: 160 },
    cell: ({ row }) => <span className="text-muted-foreground">{row.original.hostname}</span>,
  },
  {
    id: 'load',
    header: 'Load',
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
    meta: { minWidth: 110 },
    cell: ({ row }) => (
      <span className="text-xs text-muted-foreground">{relativeTime(row.original.last_seen)}</span>
    ),
  },
]

export default function WorkersPage() {
  const [workers, setWorkers] = useState<Worker[]>([])
  const [loading, setLoading] = useState(true)
  const intervalRef = useRef<ReturnType<typeof setInterval>>(0 as unknown as ReturnType<typeof setInterval>)

  const load = () =>
    listWorkers()
      .then(setWorkers)
      .catch(() => {})
      .finally(() => setLoading(false))

  useEffect(() => {
    load()
    intervalRef.current = setInterval(load, 5000)
    return () => clearInterval(intervalRef.current)
  }, [])

  return (
    <DataPage>
      <DataPage.Header>
        <DataPage.TitleBlock
          title="Workers"
          description="Registered worker nodes polling this master."
        />
      </DataPage.Header>
      <DataPage.Content>
        {loading ? (
          <div className="py-8 text-sm text-muted-foreground">Loading…</div>
        ) : workers.length === 0 ? (
          <div className="py-8 text-sm text-muted-foreground">No workers registered.</div>
        ) : (
          <DataPage.Group surface="none" className="h-full">
            <DataPage.GroupBody className="h-full [&_.dg-shell]:h-full [&_.dg-table-wrapper]:min-h-0 [&_.dg-table-wrapper]:flex-1">
              <DataGrid
                data={workers}
                columns={columns}
                tableWidthMode="fill-last"
                rowHeight={44}
                pagination={{ pageSize: 20 }}
                footer={(table) => (
                  <div className="flex h-9 items-center justify-between px-1 text-xs text-muted-foreground">
                    <span>{workers.length} workers</span>
                    <DataGridPaginationCompact table={table} />
                  </div>
                )}
              />
            </DataPage.GroupBody>
          </DataPage.Group>
        )}
      </DataPage.Content>
    </DataPage>
  )
}
