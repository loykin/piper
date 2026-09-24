import { useState } from 'react'
import { RefreshCw, Search, Square } from 'lucide-react'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { IconButton } from '@/components/ui/icon-button'
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
import { DataBodyTemplate, PageTopBar } from '@loykin/designkit'
import { FilterInput } from '@loykin/filter-input'
import { useServicesPaged, useStopService, useRestartService } from '@/features/serving/hooks'
import { ServingDetailPanel } from '@/features/serving/components/ServingDetailPanel'
import { serviceColumns } from '@/features/serving/columns'
import { RowActions } from '@/shared/components/RowActions'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { useProjectId } from '@/features/projects/context'
import type { Service } from '@/features/serving/api'
import { PageCrumbs } from '@/shared/components/PageCrumbs'
import { useTextFilter } from '@/shared/hooks/useTextFilter'
import { ConfirmDialog } from '@/shared/components/ConfirmDialog'
import { CreateButton } from '@/shared/components/CreateButton'
import { useDeleteTarget } from '@/shared/hooks/useDeleteTarget'
import { errorMessage } from '@/lib/format'

const PAGE_SIZE = 20

function ServingPageInner() {
  const { open, close } = useSidePanel()
  const projectId = useProjectId()
  const [pageIndex, setPageIndex] = useState(0)
  const servicesQuery = useServicesPaged(PAGE_SIZE, pageIndex * PAGE_SIZE)
  const { data } = servicesQuery
  const total = data?.total ?? 0
  const { mutateAsync: stopService, isPending: stopping } = useStopService()
  const restart = useRestartService()
  const stopTarget = useDeleteTarget<Service>()
  const [nameFilter, setNameFilter] = useState('')
  // Filters only the current page — not server-side yet, same accepted
  // trade-off as CredentialsPage's kind filter.
  const filteredServices = useTextFilter(data?.items, nameFilter, s => s.name)

  const actionColumn: DataGridColumnDef<Service> = {
    id: 'actions',
    header: '',
    meta: { minWidth: 100 },
    cell: ({ row }) => {
      const svc = row.original
      return (
        <RowActions className="justify-start">
          {svc.status === 'running' && (
            <IconButton icon={<RefreshCw />} label="Restart"
              onClick={e => { e.stopPropagation(); restart.mutate(svc.name) }} />
          )}
          {svc.status !== 'stopped' && (
            <IconButton icon={<Square />} label="Stop"
              onClick={e => {
                e.stopPropagation()
                stopTarget.requestDelete(svc)
              }}
              className="text-destructive hover:bg-destructive/10" />
          )}
        </RowActions>
      )
    },
  }

  const columns = [...serviceColumns, actionColumn]

  return (
    <>
    <DataBodyTemplate topBar={<PageTopBar left={<PageCrumbs items={['Service', 'Serving']} />} />}
      title="Serving"
      description="Model serving endpoints deployed from pipeline artifacts."
    >
      <DataBodyTemplate.Body>
        <DataBodyTemplate.Resource
          toolbarLeft={
            <div className="w-48">
              <FilterInput
                config={{
                  key: 'serviceSearch',
                  type: 'text',
                  placeholder: 'Search services…',
                  display: { size: 'sm', leadingIcon: <Search /> },
                }}
                value={nameFilter}
                onChange={v => setNameFilter(typeof v === 'string' ? v : '')}
              />
            </div>
          }
          toolbarRight={<CreateButton verb="Deploy" noun="Service" to={`/projects/${projectId}/serving/new`} />}
          notice={(servicesQuery.isError || restart.isError) && (
            <>
              {servicesQuery.isError && (
                <QueryErrorNotice
                  message="Failed to load services"
                  error={servicesQuery.error}
                  onRetry={() => void servicesQuery.refetch()}
                />
              )}
              {restart.isError && <p className="text-sm text-destructive">{errorMessage(restart.error)}</p>}
            </>
          )}
        >
          <DataGrid
            data={filteredServices}
            columns={columns}
            emptyContent={!servicesQuery.isError && (
              <div className="py-12 text-center">
                <p className="text-sm text-muted-foreground">No services yet.</p>
                <p className="mt-1 text-xs text-muted-foreground/60">
                  Deploy a ModelService from a pipeline artifact.
                </p>
              </div>
            )}
            tableWidthMode="fill-last"
            rowHeight={48}
            rowCursor
            onRowClick={(row) => open(<ServingDetailPanel name={row.name} />, { size: 520 })}
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

    <ConfirmDialog
      open={stopTarget.open}
      onCancel={stopTarget.cancel}
      verb="Stop"
      noun="service"
      description={`"${stopTarget.target?.name}" will stop serving requests immediately.`}
      error={stopTarget.error}
      pending={stopping}
      onConfirm={() => void stopTarget.confirm(async svc => {
        await stopService(svc.name)
        // A stopped service leaves this list (it moves to Serving History).
        void close()
      })}
    />
    </>
  )
}

export default function ServingPage() {
  return (
    <SidePanelProvider defaultSize={520} defaultMinSize={380} defaultMaxSize={900}>
      <ServingPageInner />
    </SidePanelProvider>
  )
}
