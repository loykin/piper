import { useMemo, useState } from 'react'
import { Search } from 'lucide-react'
import { DataBodyTemplate, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, PageTopBar } from '@loykin/designkit'
import { DataGrid, DataGridPaginationBar } from '@loykin/gridkit'
import { FilterInput } from '@loykin/filter-input'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { useAuth } from '@/features/auth/context'
import { useCanAdminProject, useMembers, useUsers } from '@/features/access/hooks'
import { ExecutionDetailPanel } from '@/features/notebook-executions/components/ExecutionDetailPanel'
import { useExecutionPolicy, useNotebookExecutions, useUpdateExecutionPolicy } from '@/features/notebook-executions/hooks'
import type { ExecutionPolicy } from '@/features/notebook-executions/types'
import { getExecutionColumns } from '@/features/notebook-executions/columns'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { useSearchParams } from '@/lib/router'
import { PageCrumbs } from '@/shared/components/PageCrumbs'
import { MutationErrors } from '@/shared/components/MutationErrors'

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
  const members = useMembers()
  const users = useUsers(user?.system_admin === true)
  const actorNames = useMemo(() => {
    const names = new Map<string, string>()
    for (const member of members.data ?? []) {
      if (member.username) names.set(member.user_id, member.username)
    }
    for (const account of users.data ?? []) names.set(account.id, account.username)
    return names
  }, [members.data, users.data])
  const [pageIndex, setPageIndex] = useState(0)
  const [search, setSearch] = useState('')
  const query = useNotebookExecutions(PAGE_SIZE, pageIndex * PAGE_SIZE, notebookFilter)
  const policy = useExecutionPolicy()
  const updatePolicy = useUpdateExecutionPolicy()
  const trusted = capabilities?.authentication === false
  const rows = useMemo(() => {
    const values = query.data?.items ?? []
    const needle = search.trim().toLowerCase()
    if (!needle) return values
    return values.filter(item => [item.id, item.notebook_name, item.notebook_path, item.requested_by, item.status].some(value => value?.toLowerCase().includes(needle)))
  }, [query.data, search])
  const columns = useMemo(() => getExecutionColumns(actorNames), [actorNames])
  const total = query.data?.total ?? 0

  return <DataBodyTemplate topBar={<PageTopBar left={<PageCrumbs items={['Development', 'Executions']} />} />} title="Notebook Executions" description={notebookFilter ? `Executions for ${notebookFilter}. Review approvals, progress, results, and failures.` : 'Review Jupyter executions, approvals, progress, results, and failures.'}>
    <DataBodyTemplate.Body>
      <DataBodyTemplate.Resource
        toolbarLeft={<div className="w-56"><FilterInput config={{ key: 'executionSearch', type: 'text', placeholder: 'Search current page…', display: { size: 'sm', leadingIcon: <Search /> } }} value={search} onChange={value => setSearch(typeof value === 'string' ? value : '')} /></div>}
        toolbarRight={<div className="flex items-center gap-2"><span className="text-xs text-muted-foreground">Execution policy</span><Select value={policy.data?.mcp_policy ?? 'approval_required'} onValueChange={value => updatePolicy.mutate(value as ExecutionPolicy)} disabled={!canAdmin || policy.isLoading || updatePolicy.isPending}><SelectTrigger className="w-48"><SelectValue>{value => POLICY_LABELS[value as ExecutionPolicy] ?? String(value)}</SelectValue></SelectTrigger><SelectContent><SelectItem value="disabled">Disabled</SelectItem><SelectItem value="approval_required">Approval required</SelectItem><SelectItem value="allowed">Allowed</SelectItem></SelectContent></Select></div>}
        notice={(query.isError || updatePolicy.isError) && (
          <>
            {query.isError && <QueryErrorNotice message="Failed to load notebook executions" error={query.error} onRetry={() => void query.refetch()} />}
            <MutationErrors of={[updatePolicy]} />
          </>
        )}
      >
        <DataGrid data={rows} columns={columns} emptyMessage={query.isError ? undefined : 'No notebook executions yet.'} tableWidthMode="fill-last" rowCursor onRowClick={execution => open(<ExecutionDetailPanel id={execution.id} canAdmin={canAdmin} canCancel={requestedBy => canAdmin || requestedBy === user?.id || trusted} actorNames={actorNames} />, { size: 580 })} pagination={{ pageSize: PAGE_SIZE, pageIndex, pageCount: Math.max(1, Math.ceil(total / PAGE_SIZE)), onPageChange: setPageIndex }} footer={table => <DataGridPaginationBar table={table} totalCount={total} />} />
      </DataBodyTemplate.Resource>
    </DataBodyTemplate.Body>
  </DataBodyTemplate>
}

export default function NotebookExecutionsPage() {
  return <SidePanelProvider defaultSize={580} defaultMinSize={420} defaultMaxSize={900}><NotebookExecutionsPageInner /></SidePanelProvider>
}
