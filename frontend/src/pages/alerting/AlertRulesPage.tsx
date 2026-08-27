import { useCallback, useMemo, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { DataBodyTemplate } from '@loykin/designkit'
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
import { FilterInput } from '@loykin/filter-input'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { Plus, Power, Search, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { IconButton } from '@/components/ui/icon-button'
import { alertRuleColumns } from '@/features/alerting/columns'
import { AlertRuleDetailPanel } from '@/features/alerting/components/AlertRuleDetailPanel'
import { useAlertRules, useDeleteAlertRule, usePatchAlertRule } from '@/features/alerting/hooks'
import type { AlertRule } from '@/features/alerting/types'
import { useProjectId } from '@/lib/projectContext'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { RowActions } from '@/shared/components/RowActions'

const PAGE_SIZE = 20
function AlertRulesPageInner() {
  const projectId = useProjectId(); const navigate = useNavigate(); const { open } = useSidePanel(); const [pageIndex, setPageIndex] = useState(0); const query = useAlertRules(PAGE_SIZE, pageIndex * PAGE_SIZE); const patch = usePatchAlertRule(); const remove = useDeleteAlertRule(); const [search, setSearch] = useState(''); const [deleteTarget, setDeleteTarget] = useState<AlertRule | null>(null); const [error, setError] = useState('')
  const rows = useMemo(() => { const values = query.data?.rules ?? []; const q = search.trim().toLowerCase(); return q ? values.filter(rule => rule.name.toLowerCase().includes(q)) : values }, [query.data, search])
  const toggle = useCallback(async (rule: AlertRule) => { setError(''); try { await patch.mutateAsync({ id: rule.id, request: { enabled: !rule.enabled } }) } catch (err) { setError(err instanceof Error ? err.message : String(err)) } }, [patch])
  async function confirmDelete() { if (!deleteTarget) return; setError(''); try { await remove.mutateAsync(deleteTarget.id); setDeleteTarget(null) } catch (err) { setError(err instanceof Error ? err.message : String(err)) } }
  const columns = useMemo<DataGridColumnDef<AlertRule>[]>(() => [...alertRuleColumns, { id: 'status', header: 'Status', meta: { minWidth: 90 }, cell: ({ row }) => row.original.enabled ? 'Enabled' : 'Disabled' }, { id: 'actions', header: '', meta: { minWidth: 90, align: 'right' }, cell: ({ row }) => <RowActions><IconButton icon={<Power />} label={row.original.enabled ? 'Disable' : 'Enable'} onClick={event => { event.stopPropagation(); void toggle(row.original) }} /><IconButton icon={<Trash2 />} label="Delete" className="text-destructive" onClick={event => { event.stopPropagation(); setDeleteTarget(row.original) }} /></RowActions> }], [toggle])
  const total = query.data?.total ?? 0
  return <DataBodyTemplate title="Alert Rules" description="Project-scoped event and metric notifications."><DataBodyTemplate.Body><DataBodyTemplate.Resource toolbarLeft={<div className="w-48"><FilterInput config={{ key: 'alertSearch', type: 'text', placeholder: 'Search rules…', display: { size: 'sm', leadingIcon: <Search /> } }} value={search} onChange={value => setSearch(typeof value === 'string' ? value : '')} /></div>} toolbarRight={<Button size="sm" onClick={() => void navigate({ to: `/projects/${projectId}/alert-rules/new` })}><Plus className="mr-2 size-4" />New Rule</Button>} notice={(query.isError || error) && <>{query.isError && <QueryErrorNotice message="Failed to load alert rules" error={query.error} onRetry={() => void query.refetch()} />}{error && <p className="text-sm text-destructive">{error}</p>}</>}><DataGrid data={rows} columns={columns} isLoading={query.isLoading} emptyMessage={query.isError ? undefined : 'No alert rules configured.'} tableWidthMode="fill-last" rowCursor onRowClick={rule => open(<AlertRuleDetailPanel rule={rule} onToggle={value => void toggle(value)} onDelete={setDeleteTarget} />, { size: 500 })} pagination={{ pageSize: PAGE_SIZE, pageIndex, pageCount: Math.max(1, Math.ceil(total / PAGE_SIZE)), onPageChange: setPageIndex }} footer={table => <DataGridPaginationBar table={table} totalCount={total} />} /></DataBodyTemplate.Resource></DataBodyTemplate.Body>
    <Dialog open={!!deleteTarget} onOpenChange={value => { if (!value) setDeleteTarget(null) }}><DialogContent><DialogHeader><DialogTitle>Delete {deleteTarget?.name}</DialogTitle><DialogDescription>This permanently removes the alert rule.</DialogDescription></DialogHeader><DialogFooter><Button variant="outline" onClick={() => setDeleteTarget(null)}>Cancel</Button><Button variant="destructive" disabled={remove.isPending} onClick={() => void confirmDelete()}>Delete</Button></DialogFooter></DialogContent></Dialog>
  </DataBodyTemplate>
}
export default function AlertRulesPage() { return <SidePanelProvider defaultSize={500} defaultMinSize={380} defaultMaxSize={800}><AlertRulesPageInner /></SidePanelProvider> }
