import { useEffect, useMemo, useState } from 'react'
import { RotateCcw, RefreshCw, Search, Trash2 } from 'lucide-react'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
import { DataBodyTemplate } from '@loykin/designkit'
import { FilterInput } from '@loykin/filter-input'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
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
  const total = data?.total ?? 0
  const { data: schedules = [] } = useSchedules()
  const { mutate: deleteRun, isPending: deleting, variables: deletingId } = useDeleteRun()
  const { mutateAsync: rerunRun } = useRerunRun()
  const [deleteTarget, setDeleteTarget] = useState<Run | null>(null)
  const [nameFilter, setNameFilter] = useState('')
  // Filters only the current page — not server-side yet, same accepted
  // trade-off as CredentialsPage's kind filter.
  const filteredRuns = useMemo(() => {
    const list = data?.runs ?? []
    if (!nameFilter.trim()) return list
    const q = nameFilter.trim().toLowerCase()
    return list.filter(r => r.pipeline_name.toLowerCase().includes(q))
  }, [data, nameFilter])

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
    setDeleteTarget(run)
  }

  function confirmDelete() {
    if (!deleteTarget) return
    deleteRun(deleteTarget.id)
    setDeleteTarget(null)
  }

  const handleRerun = async (e: React.MouseEvent, run: Run) => {
    e.stopPropagation()
    try {
      const result = await rerunRun(run.id)
      open(<RunDetailPanel id={result.run_id} />, { size: 480 })
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
    <>
    <DataBodyTemplate
      title="History"
      description="All pipeline run records. Each square in Steps represents one step's status."
    >
      <DataBodyTemplate.Body>
        <DataBodyTemplate.Resource
          toolbarLeft={
            <div className="w-48">
              <FilterInput
                config={{
                  key: 'runSearch',
                  type: 'text',
                  placeholder: 'Search by pipeline…',
                  display: { size: 'sm', leadingIcon: <Search /> },
                }}
                value={nameFilter}
                onChange={v => setNameFilter(typeof v === 'string' ? v : '')}
              />
            </div>
          }
          notice={runsQuery.isError && (
            <QueryErrorNotice
              message="Failed to load runs"
              error={runsQuery.error}
              onRetry={() => void runsQuery.refetch()}
            />
          )}
        >
          <DataGrid
            data={filteredRuns}
            columns={columns}
            emptyMessage="No runs yet."
            tableWidthMode="fill-last"
            rowHeight={44}
            rowCursor
            onRowClick={(row) => open(<RunDetailPanel id={row.id} />, { size: 480 })}
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
        </DataBodyTemplate.Resource>
      </DataBodyTemplate.Body>
    </DataBodyTemplate>

    <AlertDialog open={deleteTarget != null} onOpenChange={open => { if (!open) setDeleteTarget(null) }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete this run?</AlertDialogTitle>
          <AlertDialogDescription>
            Run {deleteTarget?.id} and its artifacts will be permanently removed.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={deleting}
            onClick={confirmDelete}
          >
            {deleting ? 'Deleting…' : 'Delete run'}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
    </>
  )
}

export default function HistoryPage() {
  return (
    <SidePanelProvider defaultSize={480} defaultMinSize={380} defaultMaxSize={900}>
      <HistoryPageInner />
    </SidePanelProvider>
  )
}
