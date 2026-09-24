import { useState } from 'react'
import { Search } from 'lucide-react'
import { DataGrid, DataGridPaginationBar } from '@loykin/gridkit'
import { DataBodyTemplate, PageTopBar } from '@loykin/designkit'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { FilterInput } from '@loykin/filter-input'
import { useServingHistoryPaged } from '@/features/serving/hooks'
import { serviceHistoryColumns } from '@/features/serving/columns'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { ServingHistoryDetailPanel } from '@/features/serving/components/ServingHistoryDetailPanel'
import { PageCrumbs } from '@/shared/components/PageCrumbs'
import { useTextFilter } from '@/shared/hooks/useTextFilter'

const PAGE_SIZE = 20

function ServingHistoryPageInner() {
  const { open } = useSidePanel()
  const [pageIndex, setPageIndex] = useState(0)
  const historyQuery = useServingHistoryPaged(PAGE_SIZE, pageIndex * PAGE_SIZE)
  const { data } = historyQuery
  const total = data?.total ?? 0
  const [nameFilter, setNameFilter] = useState('')
  // Filters only the current page — not server-side yet, same accepted
  // trade-off as CredentialsPage's kind filter.
  const filteredHistory = useTextFilter(data?.items, nameFilter, h => h.name)

  return (
    <DataBodyTemplate topBar={<PageTopBar left={<PageCrumbs items={['Service', 'Serving History']} />} />}
      title="Serving History"
      description="Past ModelService deployments."
    >
      <DataBodyTemplate.Body>
        <DataBodyTemplate.Resource
          toolbarLeft={
            <div className="w-48">
              <FilterInput
                config={{
                  key: 'historySearch',
                  type: 'text',
                  placeholder: 'Search history…',
                  display: { size: 'sm', leadingIcon: <Search /> },
                }}
                value={nameFilter}
                onChange={v => setNameFilter(typeof v === 'string' ? v : '')}
              />
            </div>
          }
          notice={historyQuery.isError && (
            <QueryErrorNotice
              message="Failed to load serving history"
              error={historyQuery.error}
              onRetry={() => void historyQuery.refetch()}
            />
          )}
        >
          <DataGrid
            data={filteredHistory}
            columns={serviceHistoryColumns}
            isLoading={historyQuery.isPending && data === undefined}
            emptyMessage={historyQuery.isError ? undefined : 'No deployment history yet.'}
            tableWidthMode="fill-last"
            rowHeight={44}
            rowCursor
            onRowClick={(entry) => open(<ServingHistoryDetailPanel entry={entry} />, { size: 560 })}
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

export default function ServingHistoryPage() {
  return (
    <SidePanelProvider defaultSize={560} defaultMinSize={420} defaultMaxSize={1000}>
      <ServingHistoryPageInner />
    </SidePanelProvider>
  )
}
