import { useMemo } from 'react'
import { RotateCcw, RefreshCw, Trash2 } from 'lucide-react'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { DataGrid, DataGridPaginationCompact, type DataGridColumnDef } from '@loykin/gridkit'
import { DataBodyTemplate } from '@loykin/designkit'
import { IconButton } from '@/components/ui/icon-button'
import { runColumns } from '@/features/runs/columns'
import { RunDetailPanel } from '@/features/runs/components/RunDetailPanel'
import { useRuns, useDeleteRun, useRerunRun } from '@/features/runs/hooks'
import { useSchedules } from '@/features/schedules/hooks'
import type { Run } from '@/features/runs/api'

function HistoryPageInner() {
  const { open } = useSidePanel()
  const { data: runs = [], isLoading } = useRuns()
  const { data: schedules = [] } = useSchedules()
  const { mutate: deleteRun, isPending: deleting, variables: deletingId } = useDeleteRun()
  const { mutateAsync: rerunRun } = useRerunRun()

  const scheduleById = useMemo(
    () => new Map(schedules.map(s => [s.id, s])),
    [schedules],
  )

  const handleDelete = (e: React.MouseEvent, run: Run) => {
    e.stopPropagation()
    if (!confirm(`Delete run ${run.id}?\nArtifacts will also be removed.`)) return
    deleteRun(run.id)
  }

  const handleRerun = async (e: React.MouseEvent, run: Run) => {
    e.stopPropagation()
    try {
      const result = await rerunRun(run.id)
      open(<RunDetailPanel id={result.run_id} />, { size: 720 })
    } catch (err) {
      alert(err instanceof Error ? err.message : String(err))
    }
  }

  const actionColumn: DataGridColumnDef<Run> = {
    id: 'actions',
    header: '',
    meta: { minWidth: 140, align: 'right' },
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

  const scheduleColumn: DataGridColumnDef<Run> = useMemo(() => ({
    id: 'schedule',
    header: 'Schedule',
    meta: { minWidth: 140 },
    cell: ({ row }) => {
      if (!row.original.schedule_id) return <span className="text-xs text-muted-foreground">—</span>
      const sc = scheduleById.get(row.original.schedule_id)
      return <span className="block truncate text-xs">{sc?.name ?? row.original.schedule_id.slice(0, 8)}</span>
    },
  }), [scheduleById])

  const columns = useMemo(
    () => [scheduleColumn, ...runColumns, actionColumn],
    [scheduleColumn, deleting, deletingId],
  )

  return (
    <DataBodyTemplate
      title="History"
      description="All pipeline run records. Each square in Steps represents one step's status."
    >
      <DataBodyTemplate.Body>
        <DataGrid
          data={runs}
          columns={columns}
          isLoading={isLoading}
          emptyMessage="No runs yet."
          tableWidthMode="fill-last"
          rowHeight={44}
          rowCursor
          onRowClick={(row) => open(<RunDetailPanel id={row.id} />, { size: 720 })}
          initialSorting={[{ id: 'started_at', desc: true }]}
          pagination={{ pageSize: 20 }}
          footer={(table) => (
            <div className="flex h-9 items-center justify-between px-1 text-xs text-muted-foreground">
              <span>{runs.length} results</span>
              <DataGridPaginationCompact table={table} />
            </div>
          )}
        />
      </DataBodyTemplate.Body>
    </DataBodyTemplate>
  )
}

export default function HistoryPage() {
  return (
    <SidePanelProvider defaultSize={720} defaultMinSize={520} defaultMaxSize={1200}>
      <HistoryPageInner />
    </SidePanelProvider>
  )
}
