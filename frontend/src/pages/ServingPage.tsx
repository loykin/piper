import { useState } from 'react'
import { RefreshCw, Square } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { DataGrid, DataGridPaginationCompact, type DataGridColumnDef } from '@loykin/gridkit'
import { DataBodyTemplate } from '@loykin/designkit'
import { useServices, useStopService, useRestartService } from '@/features/serving/hooks'
import { DeployForm } from '@/features/serving/components/DeployForm'
import { serviceColumns } from '@/features/serving/columns'
import type { Service } from '@/features/serving/api'

export default function ServingPage() {
  const { data: services = [], isLoading } = useServices()
  const { mutate: stopService } = useStopService()
  const { mutate: restartService } = useRestartService()
  const [showDeploy, setShowDeploy] = useState(false)

  const actionColumn: DataGridColumnDef<Service> = {
    id: 'actions',
    header: '',
    meta: { minWidth: 140 },
    cell: ({ row }) => {
      const svc = row.original
      return (
        <div className="flex items-center gap-0.5">
          {svc.status === 'running' && (
            <IconButton icon={<RefreshCw />} label="Restart"
              onClick={e => { e.stopPropagation(); restartService(svc.name) }} />
          )}
          {svc.status !== 'stopped' && (
            <IconButton icon={<Square />} label="Stop"
              onClick={e => {
                e.stopPropagation()
                if (!confirm(`Stop service "${svc.name}"?`)) return
                stopService(svc.name)
              }}
              className="text-destructive hover:bg-destructive/10" />
          )}
        </div>
      )
    },
  }

  const columns = [...serviceColumns, actionColumn]

  return (
    <DataBodyTemplate
      title="Serving"
      description="Model serving endpoints deployed from pipeline artifacts."
      actions={
        !showDeploy ? (
          <Button size="sm" onClick={() => setShowDeploy(true)}>Deploy</Button>
        ) : undefined
      }
    >
      <DataBodyTemplate.Body>
        {showDeploy && (
          <DeployForm onClose={() => setShowDeploy(false)} onDeployed={() => setShowDeploy(false)} />
        )}

        {isLoading ? (
          <div className="py-8 text-sm text-muted-foreground">Loading…</div>
        ) : services.length === 0 ? (
          <div className="py-12 text-center">
            <p className="text-sm text-muted-foreground">No services deployed yet.</p>
            <p className="mt-1 text-xs text-muted-foreground/60">
              Deploy a ModelService from a pipeline artifact.
            </p>
          </div>
        ) : (
          <DataGrid
            data={services}
            columns={columns}
            tableWidthMode="fill-last"
            rowHeight={48}
            pagination={{ pageSize: 20 }}
            footer={(table) => (
              <div className="flex h-9 items-center justify-between px-1 text-xs text-muted-foreground">
                <span>{services.length} services</span>
                <DataGridPaginationCompact table={table} />
              </div>
            )}
          />
        )}
      </DataBodyTemplate.Body>
    </DataBodyTemplate>
  )
}
