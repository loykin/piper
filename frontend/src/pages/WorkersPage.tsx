import { DataGrid, DataGridPaginationCompact, type DataGridColumnDef } from '@loykin/gridkit'
import { DataPage } from '@loykin/designkit'
import { useWorkers } from '@/features/workers/hooks'
import { useNotebookWorkers } from '@/features/notebooks/hooks'
import { useServingWorkers } from '@/features/serving/hooks'
import { pipelineColumns, nodeColumns, type NodeInfo } from '@/features/workers/columns'
import type { Worker } from '@/features/workers/api'

function WorkerSection<T extends object>({
  title, rows, cols, emptyMsg,
}: {
  title: string
  rows: T[]
  cols: DataGridColumnDef<T>[]
  emptyMsg: string
}) {
  return (
    <DataPage.Group>
      <DataPage.GroupHeader title={title} className="px-4 pt-3" />
      <DataPage.GroupBody>
        {rows.length === 0 ? (
          <div className="px-4 pb-4 text-sm text-muted-foreground">{emptyMsg}</div>
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
      </DataPage.GroupBody>
    </DataPage.Group>
  )
}

export default function WorkersPage() {
  const { data: workers = [], isLoading: l1 } = useWorkers()
  const { data: notebookNodes = [], isLoading: l2 } = useNotebookWorkers()
  const { data: servingNodes = [], isLoading: l3 } = useServingWorkers()
  const loading = l1 || l2 || l3

  return (
    <DataPage>
      <DataPage.Header>
        <DataPage.TitleBlock
          title="Workers"
          description="All registered worker nodes."
        />
      </DataPage.Header>
      <DataPage.Content>
        {loading ? (
          <div className="py-8 text-sm text-muted-foreground">Loading…</div>
        ) : (
          <div className="flex flex-col gap-6">
            <WorkerSection
              title="Pipeline Workers"
              rows={workers as Worker[]}
              cols={pipelineColumns}
              emptyMsg="No pipeline workers registered."
            />
            <WorkerSection
              title="Notebook Workers"
              rows={notebookNodes as NodeInfo[]}
              cols={nodeColumns}
              emptyMsg="No notebook workers connected."
            />
            <WorkerSection
              title="Serving Workers"
              rows={servingNodes as NodeInfo[]}
              cols={nodeColumns}
              emptyMsg="No serving workers connected."
            />
          </div>
        )}
      </DataPage.Content>
    </DataPage>
  )
}
