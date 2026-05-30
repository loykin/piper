import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { DataGrid, DataGridPaginationCompact, type DataGridColumnDef } from '@loykin/gridkit'
import { DataPage } from '@loykin/designkit'
import { listServingHistory, type ServiceHistory } from '../api'
import StatusBadge from '../components/StatusBadge'

function elapsed(deployedAt: string, stoppedAt: string): string {
  const ms = new Date(stoppedAt).getTime() - new Date(deployedAt).getTime()
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  if (ms < 3_600_000) return `${(ms / 60000).toFixed(1)}m`
  return `${(ms / 3_600_000).toFixed(1)}h`
}

const columns: DataGridColumnDef<ServiceHistory>[] = [
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

export default function ServingHistoryPage() {
  const [history, setHistory] = useState<ServiceHistory[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    listServingHistory()
      .then(setHistory)
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [])

  return (
    <DataPage>
      <DataPage.Header>
        <DataPage.TitleBlock
          title="History"
          description="Past ModelService deployments."
        />
      </DataPage.Header>
      <DataPage.Content>
        {loading ? (
          <div className="py-8 text-sm text-muted-foreground">Loading…</div>
        ) : history.length === 0 ? (
          <div className="py-8 text-sm text-muted-foreground">No deployment history yet.</div>
        ) : (
          <DataPage.Group surface="none" className="h-full">
            <DataPage.GroupBody className="h-full [&_.dg-shell]:h-full [&_.dg-table-wrapper]:min-h-0 [&_.dg-table-wrapper]:flex-1">
              <DataGrid
                data={history}
                columns={columns}
                tableWidthMode="fill-last"
                rowHeight={44}
                pagination={{ pageSize: 20 }}
                footer={(table) => (
                  <div className="flex h-9 items-center justify-between px-1 text-xs text-muted-foreground">
                    <span>{history.length} records</span>
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
