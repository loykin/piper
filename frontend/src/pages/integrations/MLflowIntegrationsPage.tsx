import { useMemo, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { Plus, Search } from 'lucide-react'
import { DataBodyTemplate } from '@loykin/designkit'
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
import { FilterInput } from '@loykin/filter-input'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { Button } from '@/components/ui/button'
import { useAuth } from '@/features/auth/context'
import { useMembers } from '@/features/access/hooks'
import { MLflowIntegrationDetailPanel } from '@/features/mlflow/components/MLflowIntegrationDetailPanel'
import { useMLflowIntegrations } from '@/features/mlflow/hooks'
import type { MLflowIntegrationDetail } from '@/features/mlflow/types'
import StatusBadge from '@/shared/components/StatusBadge'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { useProjectId } from '@/lib/projectContext'

const PAGE_SIZE = 20

function MLflowIntegrationsPageInner() {
  const projectId = useProjectId()
  const navigate = useNavigate()
  const { open } = useSidePanel()
  const { user, capabilities } = useAuth()
  const members = useMembers()
  const [pageIndex, setPageIndex] = useState(0)
  const [search, setSearch] = useState('')
  const query = useMLflowIntegrations(PAGE_SIZE, pageIndex * PAGE_SIZE)
  const membership = members.data?.find(item => item.user_id === user?.id)
  const canAdmin = capabilities?.authentication === false || user?.system_admin === true || membership?.role === 'admin'
  const rows = useMemo(() => {
    const items = query.data?.integrations ?? []
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
    <DataBodyTemplate
      title="MLflow Integrations"
      description="Export Piper run state to a project-scoped MLflow Tracking Server."
      actions={canAdmin ? <Button size="sm" onClick={() => void navigate({ to: `/projects/${projectId}/integrations/mlflow/new` })}><Plus />New Integration</Button> : undefined}
    >
      <DataBodyTemplate.Body>
        <DataBodyTemplate.Resource
          toolbarLeft={<div className="w-56"><FilterInput config={{ key: 'mlflowSearch', type: 'text', placeholder: 'Search current page…', display: { size: 'sm', leadingIcon: <Search /> } }} value={search} onChange={value => setSearch(typeof value === 'string' ? value : '')} /></div>}
          notice={query.isError ? <QueryErrorNotice message="Failed to load MLflow integrations" error={query.error} onRetry={() => void query.refetch()} /> : undefined}
        >
          <DataGrid
            data={rows}
            columns={columns}
            isLoading={query.isLoading}
            emptyMessage={query.isError ? undefined : 'No MLflow integrations configured.'}
            tableWidthMode="fill-last"
            rowCursor
            onRowClick={item => open(<MLflowIntegrationDetailPanel id={item.id} canAdmin={canAdmin} />, { size: 560 })}
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
