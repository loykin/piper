import { DataGrid, DataGridPaginationCompact } from '@loykin/gridkit'
import { DataBodyTemplate } from '@loykin/designkit'
import { useWorkers } from '@/features/workers/hooks'
import { useNotebookWorkers } from '@/features/notebooks/hooks'
import { useServingWorkers } from '@/features/serving/hooks'
import { pipelineColumns, nodeColumns } from '@/features/workers/columns'

function PipelineWorkersSection() {
  const { data = [] } = useWorkers()
  return (
    <DataBodyTemplate.Group layout="stacked" title="Pipeline Workers">
      <DataGrid
        data={data}
        columns={pipelineColumns}
        emptyMessage="No pipeline workers registered."
        tableWidthMode="fill-last"
        rowHeight={44}
        pagination={{ pageSize: 20 }}
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

function NotebookWorkersSection() {
  const { data = [] } = useNotebookWorkers()
  return (
    <DataBodyTemplate.Group layout="stacked" title="Notebook Workers">
      <DataGrid
        data={data}
        columns={nodeColumns}
        emptyMessage="No notebook workers connected."
        tableWidthMode="fill-last"
        rowHeight={44}
        pagination={{ pageSize: 20 }}
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

function ServingWorkersSection() {
  const { data = [] } = useServingWorkers()
  return (
    <DataBodyTemplate.Group layout="stacked" title="Serving Workers">
      <DataGrid
        data={data}
        columns={nodeColumns}
        emptyMessage="No serving workers connected."
        tableWidthMode="fill-last"
        rowHeight={44}
        pagination={{ pageSize: 20 }}
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

export default function WorkersPage() {
  return (
    <DataBodyTemplate
      title="Workers"
      description="All registered worker nodes."
    >
      <PipelineWorkersSection />
      <NotebookWorkersSection />
      <ServingWorkersSection />
    </DataBodyTemplate>
  )
}
