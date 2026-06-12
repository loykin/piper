import { useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { RotateCcw, RefreshCw, Trash2 } from 'lucide-react'
import { DataGrid, DataGridPaginationCompact, type DataGridColumnDef } from '@loykin/gridkit'
import { DataPage } from '@loykin/designkit'
import { IconButton } from '@/components/ui/icon-button'
import { runColumns } from '@/features/runs/columns'
import { useRuns, useDeleteRun, useRerunRun } from '@/features/runs/hooks'
import type { Run } from '@/features/runs/api'

export default function HistoryPage() {
  const navigate = useNavigate()
  const { data: runs = [], isLoading } = useRuns()
  const { mutate: deleteRun, isPending: deleting, variables: deletingId } = useDeleteRun()
  const { mutateAsync: rerunRun } = useRerunRun()

  const handleDelete = (e: React.MouseEvent, run: Run) => {
    e.stopPropagation()
    if (!confirm(`Delete run ${run.id}?\nArtifacts will also be removed.`)) return
    deleteRun(run.id)
  }

  const handleRerun = async (e: React.MouseEvent, run: Run) => {
    e.stopPropagation()
    try {
      const result = await rerunRun(run.id)
      navigate(`/runs/${result.run_id}`)
    } catch (err) {
      alert(err instanceof Error ? err.message : String(err))
    }
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
          onClick={(e) => handleRerun(e, row.original)}
          className="text-yellow-400 hover:bg-yellow-400/10" />
        <IconButton icon={<Trash2 />} label="Delete"
          disabled={row.original.status === 'running' || (deleting && deletingId === row.original.id)}
          onClick={(e) => handleDelete(e, row.original)}
          className="text-destructive hover:bg-destructive/10" />
      </div>
    ),
  }

  const columns = useMemo(() => [...runColumns, actionColumn], [deleting, deletingId])

  return (
    <DataPage>
      <DataPage.Header>
        <DataPage.TitleBlock
          title="History"
          description="All pipeline run records. Each square in Steps represents one step's status."
        />
      </DataPage.Header>
      <DataPage.Content>
        {isLoading ? (
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
