import { useMemo, useState } from 'react'
import { Search } from 'lucide-react'
import { DataBodyTemplate, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@loykin/designkit'
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
import { FilterInput } from '@loykin/filter-input'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { useAuth } from '@/features/auth/context'
import { useCanAdminProject } from '@/features/access/hooks'
import { ExecutionDetailPanel } from '@/features/notebook-executions/components/ExecutionDetailPanel'
import { useExecutionPolicy, useNotebookExecutions, useUpdateExecutionPolicy } from '@/features/notebook-executions/hooks'
import type { ExecutionPolicy, NotebookExecution } from '@/features/notebook-executions/types'
import StatusBadge from '@/shared/components/StatusBadge'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { useSearchParams } from '@/lib/router'

const PAGE_SIZE = 20
const POLICY_LABELS: Record<ExecutionPolicy, string> = {
  disabled: 'Disabled',
  approval_required: 'Approval required',
  allowed: 'Allowed',
}

function NotebookExecutionsPageInner() {
  const { open } = useSidePanel()
  const [searchParams] = useSearchParams()
  const notebookFilter = searchParams.get('notebook')?.trim() || undefined
  const { user, capabilities } = useAuth()
  const canAdmin = useCanAdminProject()
  const [pageIndex, setPageIndex] = useState(0)
  const [search, setSearch] = useState('')
  const query = useNotebookExecutions(PAGE_SIZE, pageIndex * PAGE_SIZE, notebookFilter)
  const policy = useExecutionPolicy()
  const updatePolicy = useUpdateExecutionPolicy()
  const trusted = capabilities?.authentication === false
  const rows = useMemo(() => {
    const values = query.data?.executions ?? []
    const needle = search.trim().toLowerCase()
    if (!needle) return values
    return values.filter(item => [item.id, item.notebook_name, item.notebook_path, item.requested_by, item.status].some(value => value?.toLowerCase().includes(needle)))
  }, [query.data, search])
  const columns = useMemo<DataGridColumnDef<NotebookExecution>[]>(() => [
    { accessorKey: 'notebook_name', header: 'Notebook' },
    { accessorKey: 'notebook_path', header: 'Path' },
    { accessorKey: 'status', header: 'Status', cell: ({ row }) => <StatusBadge status={row.original.status} /> },
    { id: 'progress', header: 'Progress', cell: ({ row }) => `${row.original.current_cell} / ${row.original.total_cells}` },
    { accessorKey: 'requested_by', header: 'Requested by' },
    { accessorKey: 'queued_at', header: 'Queued', cell: ({ row }) => new Date(row.original.queued_at).toLocaleString() },
  ], [])
  const total = query.data?.total ?? 0

  return <DataBodyTemplate title="Notebook Executions" description={notebookFilter ? `Executions for ${notebookFilter}. Review approvals, progress, results, and failures.` : 'Review Jupyter executions, approvals, progress, results, and failures.'}>
    <DataBodyTemplate.Body>
      <DataBodyTemplate.Resource
        toolbarLeft={<div className="w-56"><FilterInput config={{ key: 'executionSearch', type: 'text', placeholder: 'Search current page…', display: { size: 'sm', leadingIcon: <Search /> } }} value={search} onChange={value => setSearch(typeof value === 'string' ? value : '')} /></div>}
        toolbarRight={<div className="flex items-center gap-2"><span className="text-xs text-muted-foreground">Execution policy</span><Select value={policy.data?.mcp_policy ?? 'approval_required'} onValueChange={value => updatePolicy.mutate(value as ExecutionPolicy)} disabled={!canAdmin || policy.isLoading || updatePolicy.isPending}><SelectTrigger className="w-48"><SelectValue>{value => POLICY_LABELS[value as ExecutionPolicy] ?? String(value)}</SelectValue></SelectTrigger><SelectContent><SelectItem value="disabled">Disabled</SelectItem><SelectItem value="approval_required">Approval required</SelectItem><SelectItem value="allowed">Allowed</SelectItem></SelectContent></Select></div>}
        notice={query.isError ? <QueryErrorNotice message="Failed to load notebook executions" error={query.error} onRetry={() => void query.refetch()} /> : undefined}
      >
        <DataGrid data={rows} columns={columns} emptyMessage={query.isError ? undefined : 'No notebook executions yet.'} tableWidthMode="fill-last" rowCursor onRowClick={execution => open(<ExecutionDetailPanel execution={execution} canAdmin={canAdmin} canCancel={canAdmin || execution.requested_by === user?.id || trusted} />, { size: 580 })} pagination={{ pageSize: PAGE_SIZE, pageIndex, pageCount: Math.max(1, Math.ceil(total / PAGE_SIZE)), onPageChange: setPageIndex }} footer={table => <DataGridPaginationBar table={table} totalCount={total} />} />
      </DataBodyTemplate.Resource>
    </DataBodyTemplate.Body>
  </DataBodyTemplate>
}

export default function NotebookExecutionsPage() {
  return <SidePanelProvider defaultSize={580} defaultMinSize={420} defaultMaxSize={900}><NotebookExecutionsPageInner /></SidePanelProvider>
}
