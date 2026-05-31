import { useEffect, useState } from 'react'
import { DataGrid, DataGridPaginationCompact } from '@loykin/gridkit'
import { DataPage } from '@loykin/designkit'
import { listServingHistory, type ServiceHistory } from '@/features/serving/api'
import { serviceHistoryColumns } from '@/features/serving/columns'

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
                columns={serviceHistoryColumns}
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
