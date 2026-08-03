import { useEffect, useMemo, useState } from 'react'
import { RotateCcw, RefreshCw, Trash2 } from 'lucide-react'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
import { DataBodyTemplate } from '@loykin/designkit'
import { IconButton } from '@/components/ui/icon-button'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { runColumns } from '@/features/runs/columns'
import { RunDetailPanel } from '@/features/runs/components/RunDetailPanel'
import { useRunsPaged, useDeleteRun, useRerunRun } from '@/features/runs/hooks'
import { useSchedules } from '@/features/schedules/hooks'
import { RowActions } from '@/shared/components/RowActions'
import type { Run } from '@/features/runs/api'

const PAGE_SIZE = 20

function HistoryPageInner() {
  const { open } = useSidePanel()
  const [pageIndex, setPageIndex] = useState(0)
  const runsQuery = useRunsPaged({ include_steps: true, limit: PAGE_SIZE, offset: pageIndex * PAGE_SIZE })
  const { data } = runsQuery
  const runs = data?.runs ?? []
  const total = data?.total ?? 0
  const { data: schedules = [] } = useSchedules()
  const { mutate: deleteRun, isPending: deleting, variables: deletingId } = useDeleteRun()
  const { mutateAsync: rerunRun } = useRerunRun()

  // Deleting the last row of the last page shrinks `total` below what
  // pageIndex needs, leaving the grid showing an empty page. This
  // synchronizes local pagination state with that external total once it's
  // known — not derivable during render, since the offset used to fetch
  // `data` in the first place depends on the very pageIndex being corrected.
  useEffect(() => {
    if (data === undefined) return
    const pageCount = Math.max(1, Math.ceil(total / PAGE_SIZE))
    if (pageIndex > pageCount - 1) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- correcting local page state to stay in range of a total that just changed underneath it, not a derived-render substitute
      setPageIndex(pageCount - 1)
    }
  }, [data, total, pageIndex])

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
      <RowActions>
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
      </RowActions>
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
        {runsQuery.isError && runsQuery.data === undefined && (
          <QueryErrorNotice
            message="Failed to load runs"
            error={runsQuery.error}
            onRetry={() => void runsQuery.refetch()}
          />
        )}
        <DataGrid
          data={runs}
          columns={columns}
          emptyMessage="No runs yet."
          tableWidthMode="fill-last"
          rowHeight={44}
          rowCursor
          onRowClick={(row) => open(<RunDetailPanel id={row.id} />, { size: 720 })}
          initialSorting={[{ id: 'started_at', desc: true }]}
          classNames={{ footer: 'pt-3' }}
          pagination={{
            pageSize: PAGE_SIZE,
            pageIndex,
            pageCount: Math.ceil(total / PAGE_SIZE),
            onPageChange: setPageIndex,
          }}
          footer={(table) => <DataGridPaginationBar table={table} totalCount={total} />}
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
