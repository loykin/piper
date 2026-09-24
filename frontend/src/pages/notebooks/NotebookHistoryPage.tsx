import { useState } from 'react'
import { Search } from 'lucide-react'
import { DataGrid, DataGridPaginationBar } from '@loykin/gridkit'
import { DataBodyTemplate, PageTopBar } from '@loykin/designkit'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { FilterInput } from '@loykin/filter-input'
import { useNotebookHistoryPaged } from '@/features/notebooks/hooks'
import { notebookHistoryColumns } from '@/features/notebooks/columns'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { NotebookHistoryDetailPanel } from '@/features/notebooks/components/NotebookHistoryDetailPanel'
import { PageCrumbs } from '@/shared/components/PageCrumbs'
import { useTextFilter } from '@/shared/hooks/useTextFilter'

const PAGE_SIZE = 20

function NotebookHistoryPageInner() {
  const { open } = useSidePanel()
  const [pageIndex, setPageIndex] = useState(0)
  const historyQuery = useNotebookHistoryPaged(PAGE_SIZE, pageIndex * PAGE_SIZE)
  const { data } = historyQuery
  const total = data?.total ?? 0
  const [nameFilter, setNameFilter] = useState('')
  // Filters only the current page — not server-side yet, same accepted
  // trade-off as CredentialsPage's kind filter.
  const filteredHistory = useTextFilter(data?.items, nameFilter, h => h.name)

  return (
    <DataBodyTemplate topBar={<PageTopBar left={<PageCrumbs items={['Development', 'Notebook History']} />} />}
      title="Notebook History"
      description="Past notebook server runs."
    >
      <DataBodyTemplate.Body>
        <DataBodyTemplate.Resource
          toolbarLeft={
            <div className="w-48">
              <FilterInput
                config={{
                  key: 'notebookHistorySearch',
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
              message="Failed to load notebook history"
              error={historyQuery.error}
              onRetry={() => void historyQuery.refetch()}
            />
          )}
        >
          <DataGrid
            data={filteredHistory}
            columns={notebookHistoryColumns}
            isLoading={historyQuery.isPending && data === undefined}
            emptyMessage={historyQuery.isError ? undefined : 'No notebook history yet.'}
            tableWidthMode="fill-last"
            rowHeight={44}
            rowCursor
            onRowClick={(entry) => open(<NotebookHistoryDetailPanel entry={entry} />, { size: 560 })}
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

export default function NotebookHistoryPage() {
  return (
    <SidePanelProvider defaultSize={560} defaultMinSize={420} defaultMaxSize={1000}>
      <NotebookHistoryPageInner />
    </SidePanelProvider>
  )
}
