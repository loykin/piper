import { useState } from 'react'
import { DataGrid, DataGridPaginationBar } from '@loykin/gridkit'
import { DataBodyTemplate } from '@loykin/designkit'
import { useServingHistoryPaged } from '@/features/serving/hooks'
import { serviceHistoryColumns } from '@/features/serving/columns'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'

const PAGE_SIZE = 20

export default function ServingHistoryPage() {
  const [pageIndex, setPageIndex] = useState(0)
  const historyQuery = useServingHistoryPaged(PAGE_SIZE, pageIndex * PAGE_SIZE)
  const { data } = historyQuery
  const history = data?.history ?? []
  const total = data?.total ?? 0

  return (
    <DataBodyTemplate
      title="History"
      description="Past ModelService deployments."
    >
      <DataBodyTemplate.Body>
        <DataBodyTemplate.Resource
          notice={historyQuery.isError && (
            <QueryErrorNotice
              message="Failed to load serving history"
              error={historyQuery.error}
              onRetry={() => void historyQuery.refetch()}
            />
          )}
        >
          <DataGrid
            data={history}
            columns={serviceHistoryColumns}
            isLoading={historyQuery.isPending && data === undefined}
            emptyMessage="No deployment history yet."
            tableWidthMode="fill-last"
            rowHeight={44}
            classNames={{ footer: 'pt-3' }}
            pagination={{
              pageSize: PAGE_SIZE,
              pageIndex,
              pageCount: Math.max(1, Math.ceil(total / PAGE_SIZE)),
              onPageChange: setPageIndex,
            }}
            footer={(table) => <DataGridPaginationBar table={table} totalCount={total} />}
          />
        </DataBodyTemplate.Resource>
      </DataBodyTemplate.Body>
    </DataBodyTemplate>
  )
}
