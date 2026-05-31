import { useEffect, useRef, useState } from 'react'
import { DataGrid, DataGridPaginationCompact, type DataGridColumnDef } from '@loykin/gridkit'
import { DataPage } from '@loykin/designkit'
import { listWorkers, type Worker } from '@/features/workers/api'
import { listNotebookWorkers, type NotebookWorkerInfo } from '@/features/notebooks/api'
import { listServingWorkers, type ServingWorkerInfo } from '@/features/serving/api'
import { pipelineColumns, nodeColumns, type NodeInfo } from '@/features/workers/columns'

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
  const [workers, setWorkers] = useState<Worker[]>([])
  const [notebookNodes, setNotebookNodes] = useState<NotebookWorkerInfo[]>([])
  const [servingNodes, setServingNodes] = useState<ServingWorkerInfo[]>([])
  const [loading, setLoading] = useState(true)
  const intervalRef = useRef<ReturnType<typeof setInterval>>(0 as unknown as ReturnType<typeof setInterval>)

  const load = () => {
    Promise.all([
      listWorkers().catch(() => [] as Worker[]),
      listNotebookWorkers().catch(() => [] as NotebookWorkerInfo[]),
      listServingWorkers().catch(() => [] as ServingWorkerInfo[]),
    ]).then(([w, nb, sv]) => {
      setWorkers(w)
      setNotebookNodes(nb)
      setServingNodes(sv)
    }).finally(() => setLoading(false))
  }

  useEffect(() => {
    load()
    intervalRef.current = setInterval(load, 5000)
    return () => clearInterval(intervalRef.current)
  }, [])

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
              rows={workers}
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
