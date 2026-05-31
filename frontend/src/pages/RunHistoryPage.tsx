import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { RotateCcw, RefreshCw, Trash2 } from 'lucide-react'
import { DataGrid, DataGridPaginationCompact, type DataGridColumnDef } from '@loykin/gridkit'
import { DataPage } from '@loykin/designkit'
import { IconButton } from '@/components/ui/icon-button'
import { listRuns, deleteRun, rerunRun, type Run } from '@/features/runs/api'
import { runColumns } from '@/features/runs/columns'

export default function RunHistoryPage() {
  const [runs, setRuns] = useState<Run[]>([])
  const [loading, setLoading] = useState(true)
  const [deleting, setDeleting] = useState<string | null>(null)
  const navigate = useNavigate()
  const intervalRef = useRef<ReturnType<typeof setInterval>>(0 as unknown as ReturnType<typeof setInterval>)

  const load = () =>
    listRuns()
      .then(setRuns)
      .catch(() => {})
      .finally(() => setLoading(false))

  useEffect(() => {
    load()
    intervalRef.current = setInterval(load, 3000)
    return () => clearInterval(intervalRef.current)
  }, [])

  const handleDelete = (e: React.MouseEvent, run: Run) => {
    e.stopPropagation()
    if (!confirm(`Delete run ${run.id}?\nArtifacts will also be removed.`)) return
    setDeleting(run.id)
    deleteRun(run.id)
      .then(load)
      .catch((err) => alert(err.message))
      .finally(() => setDeleting(null))
  }

  const handleRerun = (e: React.MouseEvent, run: Run, failedOnly = false) => {
    e.stopPropagation()
    rerunRun(run.id, failedOnly)
      .then(({ run_id }) => navigate(`/runs/${run_id}`))
      .catch((err) => alert(err.message))
  }

  const actionColumn: DataGridColumnDef<Run> = {
    id: 'actions',
    header: '',
    meta: { minWidth: 210, align: 'right' },
    cell: ({ row }) => (
      <div className="flex justify-end items-center gap-0.5">
        <IconButton icon={<RotateCcw />} label="Rerun"
          disabled={row.original.status === 'running' || row.original.status === 'scheduled'}
          onClick={(e) => handleRerun(e, row.original)}
          className="text-primary hover:bg-primary/10" />
        <IconButton icon={<RefreshCw />} label="Retry Failed"
          disabled={row.original.status !== 'failed'}
          onClick={(e) => handleRerun(e, row.original, true)}
          className="text-yellow-400 hover:bg-yellow-400/10" />
        <IconButton icon={<Trash2 />} label="Delete"
          disabled={row.original.status === 'running' || deleting === row.original.id}
          onClick={(e) => handleDelete(e, row.original)}
          className="text-destructive hover:bg-destructive/10" />
      </div>
    ),
  }

  const columns = useMemo(() => [...runColumns, actionColumn], [deleting])

  return (
    <DataPage>
      <DataPage.Header>
        <DataPage.TitleBlock
          title="Run History"
          description="All pipeline runs. Each square in Steps represents one step's status."
        />
      </DataPage.Header>
      <DataPage.Content>
        {loading ? (
          <div className="py-8 text-sm text-muted-foreground">Loading…</div>
        ) : runs.length === 0 ? (
          <div className="py-8 text-sm text-muted-foreground">No runs yet.</div>
        ) : (
          <DataPage.Group surface="none" className="h-full">
            <DataPage.GroupBody className="h-full [&_.dg-shell]:h-full [&_.dg-table-wrapper]:min-h-0 [&_.dg-table-wrapper]:flex-1">
              <DataGrid
                data={runs}
                columns={columns}
                tableWidthMode="fill-last"
                rowHeight={44}
                rowCursor
                onRowClick={(row) => navigate(`/runs/${row.id}`)}
                pagination={{ pageSize: 20 }}
                footer={(table) => (
                  <div className="flex h-9 items-center justify-between px-1 text-xs text-muted-foreground">
                    <span>{runs.length} results</span>
                    <DataGridPaginationCompact table={table} />
                  </div>
                )}
              />
            </DataPage.GroupBody>
          </DataPage.Group>
        )}
      </DataPage.Content>
    </DataPage>
  )
}
