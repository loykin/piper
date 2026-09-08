import { useMemo, useState } from 'react'
import { Plus, Search } from 'lucide-react'
import { useNavigate } from '@tanstack/react-router'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
import { DataBodyTemplate } from '@loykin/designkit'
import { FilterInput } from '@loykin/filter-input'
import { useExperimentsPaged } from '@/features/runs/hooks'
import { ExperimentDetailPanel } from '@/features/runs/components/ExperimentDetailPanel'
import type { ExperimentSummary } from '@/features/runs/types'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { Button } from '@/components/ui/button'
import { useProjectId } from '@/lib/projectContext'

const PAGE_SIZE = 25

function ExperimentsPageInner() {
  const { open } = useSidePanel()
  const navigate = useNavigate()
  const projectId = useProjectId()
  const [nameFilter, setNameFilter] = useState('')
  const [pageIndex, setPageIndex] = useState(0)
  const query = useExperimentsPaged(nameFilter.trim(), PAGE_SIZE, pageIndex * PAGE_SIZE)
  const experiments = query.data?.experiments ?? []
  const total = query.data?.total ?? 0

  const columns = useMemo<DataGridColumnDef<ExperimentSummary>[]>(() => [
    { id: 'name',    header: 'Experiment',  accessorKey: 'name',    meta: { minWidth: 220 } },
    { id: 'runs',    header: 'Runs',        accessorKey: 'runs',    meta: { minWidth: 80 } },
    { id: 'success', header: 'Success',     accessorKey: 'success', meta: { minWidth: 80 },
      cell: ({ row }) => <span className="text-green-400">{row.original.success}</span> },
    { id: 'failed',  header: 'Failed',      accessorKey: 'failed',  meta: { minWidth: 80 },
      cell: ({ row }) => row.original.failed > 0
        ? <span className="text-red-400">{row.original.failed}</span>
        : <span>{row.original.failed}</span> },
    { id: 'running', header: 'Running',     accessorKey: 'running', meta: { minWidth: 80 },
      cell: ({ row }) => row.original.running > 0
        ? <span className="text-blue-400">{row.original.running}</span>
        : <span>{row.original.running}</span> },
    { id: 'latest',  header: 'Latest Run',  accessorKey: 'latest',
      cell: ({ row }) => (
        <span className="text-muted-foreground text-xs">
          {new Date(row.original.latest).toLocaleString()}
        </span>
      ) },
  ], [])

  return (
    <DataBodyTemplate
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
          toolbarRight={
            <Button size="sm" onClick={() => void navigate({ to: `/projects/${projectId}/experiments/new` })}>
              <Plus className="mr-2 size-4" />New Sweep
            </Button>
          }
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
            columns={columns}
            isLoading={query.isPending}
            emptyMessage={query.isError ? undefined : 'No experiments yet. Create a sweep to compare parameter trials.'}
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
