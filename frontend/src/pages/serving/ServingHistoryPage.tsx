import { DataGrid, DataGridPaginationCompact } from '@loykin/gridkit'
import { DataBodyTemplate } from '@loykin/designkit'
import { useServingHistory } from '@/features/serving/hooks'
import { serviceHistoryColumns } from '@/features/serving/columns'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'

export default function ServingHistoryPage() {
  const historyQuery = useServingHistory()
  const history = historyQuery.data ?? []

  return (
    <DataBodyTemplate
      title="History"
      description="Past ModelService deployments."
    >
      <DataBodyTemplate.Body>
        {historyQuery.isError && (
          <QueryErrorNotice
            message="Failed to load serving history"
            error={historyQuery.error}
            onRetry={() => void historyQuery.refetch()}
          />
        )}
        <DataGrid
          data={history}
          columns={serviceHistoryColumns}
          isLoading={historyQuery.isLoading}
          emptyMessage="No deployment history yet."
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
      </DataBodyTemplate.Body>
    </DataBodyTemplate>
  )
}
