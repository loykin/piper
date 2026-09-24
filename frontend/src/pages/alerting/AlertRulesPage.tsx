import { useCallback, useMemo, useState } from 'react'
import { DataBodyTemplate, PageTopBar } from '@loykin/designkit'
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
import { FilterInput } from '@loykin/filter-input'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { Power, Search, Trash2 } from 'lucide-react'
import { IconButton } from '@/components/ui/icon-button'
import { alertRuleColumns } from '@/features/alerting/columns'
import { AlertRuleDetailPanel } from '@/features/alerting/components/AlertRuleDetailPanel'
import { useAlertRules, useDeleteAlertRule, usePatchAlertRule } from '@/features/alerting/hooks'
import type { AlertRule } from '@/features/alerting/types'
import { useProjectId } from '@/features/projects/context'
import { errorMessage } from '@/lib/format'
import { ConfirmDialog } from '@/shared/components/ConfirmDialog'
import { CreateButton } from '@/shared/components/CreateButton'
import { PageCrumbs } from '@/shared/components/PageCrumbs'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { RowActions } from '@/shared/components/RowActions'
import { useDeleteTarget } from '@/shared/hooks/useDeleteTarget'
import { useTextFilter } from '@/shared/hooks/useTextFilter'

const PAGE_SIZE = 20

function AlertRulesPageInner() {
  const projectId = useProjectId()
  const { open, close } = useSidePanel()
  const [pageIndex, setPageIndex] = useState(0)
  const query = useAlertRules(PAGE_SIZE, pageIndex * PAGE_SIZE)
  const total = query.data?.total ?? 0
  const patch = usePatchAlertRule()
  const remove = useDeleteAlertRule()
  const [search, setSearch] = useState('')
  const rows = useTextFilter(query.data?.items, search, rule => rule.name)
  const deleteTarget = useDeleteTarget<AlertRule>()
  const { requestDelete } = deleteTarget
  const [error, setError] = useState('')

  // Enable/disable is reversible, so it runs without a confirmation.
  const toggle = useCallback(async (rule: AlertRule) => {
    setError('')
    try {
      await patch.mutateAsync({ id: rule.id, request: { enabled: !rule.enabled } })
    } catch (err) {
      setError(errorMessage(err))
    }
  }, [patch])

  const columns = useMemo<DataGridColumnDef<AlertRule>[]>(() => [
    ...alertRuleColumns,
    {
      id: 'status',
      header: 'Status',
      meta: { minWidth: 90 },
      cell: ({ row }) => row.original.enabled ? 'Enabled' : 'Disabled',
    },
    {
      id: 'actions',
      header: '',
      meta: { minWidth: 90, align: 'right' },
      cell: ({ row }) => (
        <RowActions>
          <IconButton
            icon={<Power />}
            label={row.original.enabled ? 'Disable' : 'Enable'}
            onClick={event => { event.stopPropagation(); void toggle(row.original) }}
          />
          <IconButton
            icon={<Trash2 />}
            label="Delete"
            className="text-destructive hover:bg-destructive/10"
            onClick={event => { event.stopPropagation(); requestDelete(row.original) }}
          />
        </RowActions>
      ),
    },
  ], [toggle, requestDelete])

  return (
    <>
      <DataBodyTemplate
        topBar={<PageTopBar left={<PageCrumbs items={['Infrastructure', 'Alert Rules']} />} />}
        title="Alert Rules"
        description="Project-scoped event and metric notifications."
      >
        <DataBodyTemplate.Body>
          <DataBodyTemplate.Resource
            toolbarLeft={
              <div className="w-48">
                <FilterInput
                  config={{ key: 'alertSearch', type: 'text', placeholder: 'Search rules…', display: { size: 'sm', leadingIcon: <Search /> } }}
                  value={search}
                  onChange={value => setSearch(typeof value === 'string' ? value : '')}
                />
              </div>
            }
            toolbarRight={<CreateButton noun="Alert Rule" to={`/projects/${projectId}/alert-rules/new`} />}
            notice={(query.isError || error) && (
              <>
                {query.isError && <QueryErrorNotice message="Failed to load alert rules" error={query.error} onRetry={() => void query.refetch()} />}
                {error && <p className="text-sm text-destructive">{error}</p>}
              </>
            )}
          >
            <DataGrid
              data={rows}
              columns={columns}
              isLoading={query.isLoading}
              emptyMessage={query.isError ? undefined : 'No alert rules yet.'}
              tableWidthMode="fill-last"
              rowCursor
              onRowClick={rule => open(
                <AlertRuleDetailPanel id={rule.id} onToggle={value => void toggle(value)} onDelete={deleteTarget.requestDelete} />,
                { size: 500 },
              )}
              pagination={{
                pageSize: PAGE_SIZE,
                pageIndex,
                pageCount: Math.max(1, Math.ceil(total / PAGE_SIZE)),
                onPageChange: setPageIndex,
              }}
              footer={table => <DataGridPaginationBar table={table} totalCount={total} />}
            />
          </DataBodyTemplate.Resource>
        </DataBodyTemplate.Body>
      </DataBodyTemplate>
      {/* Must render outside <DataBodyTemplate> — it only mounts its own recognized sub-components (.Body/.Tab/...) as children; a plain sibling here is silently dropped. */}
      <ConfirmDialog
        open={deleteTarget.open}
        onCancel={deleteTarget.cancel}
        verb="Delete"
        noun="alert rule"
        description={`"${deleteTarget.target?.name}" will be permanently removed.`}
        error={deleteTarget.error}
        pending={remove.isPending}
        onConfirm={() => void deleteTarget.confirm(async rule => {
          await remove.mutateAsync(rule.id)
          void close()
        })}
      />
    </>
  )
}

export default function AlertRulesPage() {
  return (
    <SidePanelProvider defaultSize={500} defaultMinSize={380} defaultMaxSize={800}>
      <AlertRulesPageInner />
    </SidePanelProvider>
  )
}
