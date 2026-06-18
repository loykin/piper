import { DataGrid, DataGridPaginationCompact, type DataGridColumnDef } from '@loykin/gridkit'
import { DataBodyTemplate } from '@loykin/designkit'
import { useWorkers } from '@/features/workers/hooks'
import { useNotebookWorkers } from '@/features/notebooks/hooks'
import { useServingWorkers } from '@/features/serving/hooks'
import { pipelineColumns, nodeColumns, type NodeInfo } from '@/features/workers/columns'
import type { Worker } from '@/features/workers/api'

function WorkerSection<T extends object>({
  title, rows, cols, emptyMsg, isLoading,
}: {
  title: string
  rows: T[]
  cols: DataGridColumnDef<T>[]
  emptyMsg: string
  isLoading?: boolean
}) {
  return (
    <DataBodyTemplate.Group variant="bordered" title={title}>
      {isLoading ? (
        <div className="text-sm text-muted-foreground">Loading…</div>
      ) : rows.length === 0 ? (
        <div className="text-sm text-muted-foreground">{emptyMsg}</div>
      ) : (
        <DataGrid
          data={rows}
          columns={cols}
          tableWidthMode="fill-last"
          rowHeight={44}
          pagination={{ pageSize: 20 }}
          footer={(table) => (
            <div className="flex h-9 items-center justify-between px-1 text-xs text-muted-foreground">
              <span>{rows.length} nodes</span>
              <DataGridPaginationCompact table={table} />
            </div>
          )}
        />
      )}
    </DataBodyTemplate.Group>
  )
}

export default function WorkersPage() {
  const { data: workers = [], isLoading: l1 } = useWorkers()
  const { data: notebookNodes = [], isLoading: l2 } = useNotebookWorkers()
  const { data: servingNodes = [], isLoading: l3 } = useServingWorkers()

  return (
    <DataBodyTemplate
      title="Workers"
      description="All registered worker nodes."
    >
      <DataBodyTemplate.Body className="space-y-4">
        <WorkerSection
          title="Pipeline Workers"
          rows={workers as Worker[]}
          cols={pipelineColumns}
          emptyMsg="No pipeline workers registered."
          isLoading={l1}
        />
        <WorkerSection
          title="Notebook Workers"
          rows={notebookNodes as NodeInfo[]}
          cols={nodeColumns}
          emptyMsg="No notebook workers connected."
          isLoading={l2}
        />
        <WorkerSection
          title="Serving Workers"
          rows={servingNodes as NodeInfo[]}
          cols={nodeColumns}
          emptyMsg="No serving workers connected."
          isLoading={l3}
        />
      </DataBodyTemplate.Body>
    </DataBodyTemplate>
  )
}
