import { DataGrid, DataGridPaginationCompact } from '@loykin/gridkit'
import { DataBodyTemplate } from '@loykin/designkit'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { useWorkers } from '@/features/workers/hooks'
import { workerColumns } from '@/features/workers/columns'
import { WorkerPodPolicyPanel } from '@/features/workers/components/WorkerPodPolicyPanel'
import type { Worker } from '@/features/workers/types'

function WorkersTable() {
  const { data = [] } = useWorkers()
  const { open } = useSidePanel()
  return (
    <DataBodyTemplate.Group layout="stacked" title="Connected Workers">
      <DataGrid
        data={data}
        columns={workerColumns}
        emptyMessage="No workers connected."
        tableWidthMode="fill-last"
        rowHeight={44}
        rowCursor
        pagination={{ pageSize: 20 }}
        onRowClick={(row) =>
          open(<WorkerPodPolicyPanel worker={row as Worker} />, { size: 520 })
        }
        footer={(table) => (
          <div className="flex h-9 items-center justify-between px-1 text-xs text-muted-foreground">
            <span>{data.length} workers</span>
            <DataGridPaginationCompact table={table} />
          </div>
        )}
      />
    </DataBodyTemplate.Group>
  )
}

function WorkersContent() {
  return (
    <DataBodyTemplate
      title="Workers"
      description="All registered worker nodes."
    >
      <WorkersTable />
    </DataBodyTemplate>
  )
}

export default function WorkersPage() {
  return (
    <SidePanelProvider defaultSize={520} defaultMinSize={380} defaultMaxSize={900}>
      <WorkersContent />
    </SidePanelProvider>
  )
}
