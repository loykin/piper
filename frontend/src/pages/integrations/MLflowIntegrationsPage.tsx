import { useNavigate } from '@/lib/router'
import { useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Plus, Search } from 'lucide-react'
import { DataBodyTemplate, PageTopBar } from '@loykin/designkit'
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
import { FilterInput } from '@loykin/filter-input'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { Button } from '@/components/ui/button'
import { useCanAdminProject } from '@/features/access/hooks'
import { MLflowIntegrationDetailPanel } from '@/features/mlflow/components/MLflowIntegrationDetailPanel'
import { mlflowKeys, useMLflowIntegrations } from '@/features/mlflow/hooks'
import type { MLflowIntegrationDetail } from '@/features/mlflow/types'
import StatusBadge from '@/shared/components/StatusBadge'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { useProjectId } from '@/features/projects/context'
import { PageCrumbs } from '@/shared/components/PageCrumbs'

const PAGE_SIZE = 20

function MLflowIntegrationsPageInner() {
  const projectId = useProjectId()
  const navigate = useNavigate()
  const { open } = useSidePanel()
  const queryClient = useQueryClient()
  const canAdmin = useCanAdminProject()
  const [pageIndex, setPageIndex] = useState(0)
  const [search, setSearch] = useState('')
  const query = useMLflowIntegrations(PAGE_SIZE, pageIndex * PAGE_SIZE)
  const rows = useMemo(() => {
    const items = query.data?.items ?? []
    const needle = search.trim().toLowerCase()
    return needle
      ? items.filter(item => item.name.toLowerCase().includes(needle) || item.tracking_uri.toLowerCase().includes(needle))
      : items
  }, [query.data, search])
  const columns = useMemo<DataGridColumnDef<MLflowIntegrationDetail>[]>(() => [
    { accessorKey: 'name', header: 'Name' },
    { accessorKey: 'tracking_uri', header: 'Tracking host' },
    { id: 'scope', header: 'Export', cell: ({ row }) => [row.original.export_pipelines && 'Pipelines', row.original.export_notebook_executions && 'Notebooks'].filter(Boolean).join(', ') || 'None' },
    { id: 'state', header: 'Health', cell: ({ row }) => <StatusBadge status={row.original.health} /> },
    { id: 'backlog', header: 'Backlog', cell: ({ row }) => `${row.original.pending_events} pending · ${row.original.dead_events} dead` },
    { id: 'default', header: 'Default', cell: ({ row }) => row.original.default ? 'Yes' : 'No' },
  ], [])
  const total = query.data?.total ?? 0

  return (
    <DataBodyTemplate topBar={<PageTopBar left={<PageCrumbs items={['Infrastructure', 'Integrations', 'MLflow']} />} />}
      title="MLflow Integrations"
      description="Export Piper run state to a project-scoped MLflow Tracking Server."
    >
      <DataBodyTemplate.Body>
        <DataBodyTemplate.Resource
          toolbarLeft={<div className="w-56"><FilterInput config={{ key: 'mlflowSearch', type: 'text', placeholder: 'Search current page…', display: { size: 'sm', leadingIcon: <Search /> } }} value={search} onChange={value => setSearch(typeof value === 'string' ? value : '')} /></div>}
          toolbarRight={canAdmin ? <Button size="sm" onClick={() => void navigate(`/projects/${projectId}/integrations/mlflow/new`)}><Plus />New Integration</Button> : undefined}
          notice={query.isError ? <QueryErrorNotice message="Failed to load MLflow integrations" error={query.error} onRetry={() => void query.refetch()} /> : undefined}
        >
          <DataGrid
            data={rows}
            columns={columns}
            isLoading={query.isLoading}
            emptyMessage={query.isError ? undefined : 'No MLflow integrations configured.'}
            tableWidthMode="fill-last"
            rowCursor
            onRowClick={item => { queryClient.setQueryData(mlflowKeys.detail(projectId, item.id), item); open(<MLflowIntegrationDetailPanel id={item.id} canAdmin={canAdmin} />, { size: 560 }) }}
            pagination={{ pageSize: PAGE_SIZE, pageIndex, pageCount: Math.max(1, Math.ceil(total / PAGE_SIZE)), onPageChange: setPageIndex }}
            footer={table => <DataGridPaginationBar table={table} totalCount={total} />}
          />
        </DataBodyTemplate.Resource>
      </DataBodyTemplate.Body>
    </DataBodyTemplate>
  )
}

export default function MLflowIntegrationsPage() {
  return <SidePanelProvider defaultSize={560} defaultMinSize={420} defaultMaxSize={900}><MLflowIntegrationsPageInner /></SidePanelProvider>
}
