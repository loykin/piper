import { useState } from 'react'
import { Search } from 'lucide-react'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { DataGrid, DataGridPaginationBar } from '@loykin/gridkit'
import { DataBodyTemplate, PageTopBar } from '@loykin/designkit'
import { FilterInput } from '@loykin/filter-input'
import { useExperimentsPaged } from '@/features/runs/hooks'
import { ExperimentDetailPanel } from '@/features/runs/components/ExperimentDetailPanel'
import { experimentColumns } from '@/features/runs/columns'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { useProjectId } from '@/features/projects/context'
import { PageCrumbs } from '@/shared/components/PageCrumbs'
import { CreateButton } from '@/shared/components/CreateButton'

const PAGE_SIZE = 25

function ExperimentsPageInner() {
  const { open } = useSidePanel()
  const projectId = useProjectId()
  const [nameFilter, setNameFilter] = useState('')
  const [pageIndex, setPageIndex] = useState(0)
  const query = useExperimentsPaged(nameFilter.trim(), PAGE_SIZE, pageIndex * PAGE_SIZE)
  const experiments = query.data?.items ?? []
  const total = query.data?.total ?? 0


  return (
    <DataBodyTemplate topBar={<PageTopBar left={<PageCrumbs items={['Pipelines', 'Experiments']} />} />}
      title="Experiments"
      description="Grouped sweep runs. Click an experiment to compare runs by params and metrics."
    >
      <DataBodyTemplate.Body>
        <DataBodyTemplate.Resource
          toolbarLeft={
            <div className="w-48">
              <FilterInput
                config={{
                  key: 'experimentSearch',
                  type: 'text',
                  placeholder: 'Search experiments…',
                  display: { size: 'sm', leadingIcon: <Search /> },
                }}
                value={nameFilter}
                onChange={v => { setNameFilter(typeof v === 'string' ? v : ''); setPageIndex(0) }}
              />
            </div>
          }
          toolbarRight={<CreateButton noun="Sweep" to={`/projects/${projectId}/experiments/new`} />}
          notice={query.isError && (
            <QueryErrorNotice
              message="Failed to load experiments"
              error={query.error}
              onRetry={() => void query.refetch()}
            />
          )}
        >
          <DataGrid
            data={experiments}
            columns={experimentColumns}
            isLoading={query.isPending}
            emptyMessage={query.isError ? undefined : 'No experiments yet.'}
            tableWidthMode="fill-last"
            rowHeight={44}
            rowCursor
            onRowClick={(row) => open(<ExperimentDetailPanel experiment={row.name} />, { size: 800 })}
            pagination={{ pageSize: PAGE_SIZE, pageIndex, pageCount: Math.max(1, Math.ceil(total / PAGE_SIZE)), onPageChange: setPageIndex }}
            footer={table => <DataGridPaginationBar table={table} totalCount={total} />}
          />
        </DataBodyTemplate.Resource>
      </DataBodyTemplate.Body>
    </DataBodyTemplate>
  )
}

export default function ExperimentsPage() {
  return (
    <SidePanelProvider defaultSize={800} defaultMinSize={580} defaultMaxSize={1200}>
      <ExperimentsPageInner />
    </SidePanelProvider>
  )
}
