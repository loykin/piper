import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { RefreshCw, Square } from 'lucide-react'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
import { DataBodyTemplate } from '@loykin/designkit'
import { useServicesPaged, useStopService, useRestartService } from '@/features/serving/hooks'
import { ServingDetailPanel } from '@/features/serving/components/ServingDetailPanel'
import { serviceColumns } from '@/features/serving/columns'
import { RowActions } from '@/shared/components/RowActions'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { useProjectId } from '@/lib/projectContext'
import type { Service } from '@/features/serving/api'

const PAGE_SIZE = 20

function ServingPageInner() {
  const { open } = useSidePanel()
  const projectId = useProjectId()
  const navigate = useNavigate()
  const [pageIndex, setPageIndex] = useState(0)
  const servicesQuery = useServicesPaged(PAGE_SIZE, pageIndex * PAGE_SIZE)
  const { data } = servicesQuery
  const services = data?.services ?? []
  const total = data?.total ?? 0
  const { mutate: stopService, isPending: stopping } = useStopService()
  const { mutate: restartService } = useRestartService()
  const [stopTarget, setStopTarget] = useState<Service | null>(null)

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
              onClick={e => { e.stopPropagation(); restartService(svc.name) }} />
          )}
          {svc.status !== 'stopped' && (
            <IconButton icon={<Square />} label="Stop"
              onClick={e => {
                e.stopPropagation()
                setStopTarget(svc)
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
    <DataBodyTemplate
      title="Serving"
      description="Model serving endpoints deployed from pipeline artifacts."
    >
      <DataBodyTemplate.Body>
        <DataBodyTemplate.Resource
          toolbarRight={
            <Button size="sm" onClick={() => void navigate({ to: `/projects/${projectId}/serving/new` })}>Deploy</Button>
          }
          notice={servicesQuery.isError && (
            <QueryErrorNotice
              message="Failed to load services"
              error={servicesQuery.error}
              onRetry={() => void servicesQuery.refetch()}
            />
          )}
        >
          <DataGrid
            data={services}
            columns={columns}
            emptyContent={
              <div className="py-12 text-center">
                <p className="text-sm text-muted-foreground">No services deployed yet.</p>
                <p className="mt-1 text-xs text-muted-foreground/60">
                  Deploy a ModelService from a pipeline artifact.
                </p>
              </div>
            }
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

    <AlertDialog open={stopTarget != null} onOpenChange={open => { if (!open) setStopTarget(null) }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Stop this service?</AlertDialogTitle>
          <AlertDialogDescription>
            "{stopTarget?.name}" will stop serving requests immediately.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={stopping}
            onClick={() => {
              if (!stopTarget) return
              stopService(stopTarget.name)
              setStopTarget(null)
            }}
          >
            {stopping ? 'Stopping…' : 'Stop service'}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
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
