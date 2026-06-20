import { DataGrid, DataGridPaginationCompact } from '@loykin/gridkit'
import { DataBodyTemplate } from '@loykin/designkit'
import { useServingHistory } from '@/features/serving/hooks'
import { serviceHistoryColumns } from '@/features/serving/columns'

export default function ServingHistoryPage() {
  const { data: history = [], isLoading } = useServingHistory()

  return (
    <DataBodyTemplate
      title="History"
      description="Past ModelService deployments."
    >
      <DataBodyTemplate.Body>
        <DataGrid
          data={history}
          columns={serviceHistoryColumns}
          isLoading={isLoading}
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
