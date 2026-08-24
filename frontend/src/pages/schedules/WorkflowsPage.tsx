import { useMemo, useState } from 'react'
import { useNavigate } from '@/lib/router'
import { useProjectId } from '@/lib/projectContext'
import { Power, Plus, Search, Trash2 } from 'lucide-react'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { DataGrid, DataGridPaginationBar } from '@loykin/gridkit'
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
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { scheduleColumns } from '@/features/schedules/columns'
import { ScheduleDetailPanel } from '@/features/schedules/components/ScheduleDetailPanel'
import { useSchedulesPaged, useDeleteSchedule, useToggleSchedule } from '@/features/schedules/hooks'
import { usePipelines } from '@/features/pipelines/hooks'
import { RowActions } from '@/shared/components/RowActions'
import type { DataGridColumnDef } from '@loykin/gridkit'
import type { Schedule } from '@/features/schedules/api'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'

const PAGE_SIZE = 20

function WorkflowsPageInner() {
  const navigate = useNavigate()
  const projectId = useProjectId()
  const { open } = useSidePanel()
  const [pageIndex, setPageIndex] = useState(0)
  const schedulesQuery = useSchedulesPaged(PAGE_SIZE, pageIndex * PAGE_SIZE)
  const total = schedulesQuery.data?.total ?? 0
  const { data: pipelines = [] } = usePipelines()
  const { mutate: deleteSchedule, isPending: deleting } = useDeleteSchedule()
  const { mutate: toggleSchedule } = useToggleSchedule()
  const [deleteTarget, setDeleteTarget] = useState<Schedule | null>(null)
  const [nameFilter, setNameFilter] = useState('')
  // Filters only the current page — not server-side yet, same accepted
  // trade-off as CredentialsPage's kind filter.
  const filteredSchedules = useMemo(() => {
    const list = schedulesQuery.data?.schedules ?? []
    if (!nameFilter.trim()) return list
    const q = nameFilter.trim().toLowerCase()
    return list.filter(s => s.name.toLowerCase().includes(q))
  }, [schedulesQuery.data, nameFilter])

  const pipelineByVersionId = useMemo(
    () => new Map(pipelines.map(p => [p.id, p])),
    [pipelines],
  )

  const nameVersionColumn: DataGridColumnDef<Schedule> = useMemo(() => ({
    id: 'name',
    header: 'Name',
    meta: { flex: 1, minWidth: 160 },
    cell: ({ row }) => {
      const vid = row.original.template_version_id
      const tpl = vid ? pipelineByVersionId.get(vid) : undefined
      return (
        <span className="flex items-baseline gap-1.5">
          <span className="text-sm font-medium">{row.original.name}</span>
          {tpl && <span className="text-xs text-muted-foreground">v{tpl.version}</span>}
        </span>
      )
    },
  }), [pipelineByVersionId])

  const actionColumn: DataGridColumnDef<Schedule> = useMemo(() => ({
    id: 'actions',
    header: '',
    size: 72,
    cell: ({ row }) => {
      const s = row.original
      return (
        <RowActions>
          {s.schedule_type === 'cron' && (
            <IconButton icon={<Power />} label={s.enabled ? 'Disable' : 'Enable'}
              onClick={(e) => {
                e.stopPropagation()
                toggleSchedule({ id: s.id, enabled: !s.enabled })
              }}
              className={s.enabled ? 'text-primary hover:bg-primary/10' : ''} />
          )}
          <IconButton icon={<Trash2 />} label="Delete"
            onClick={(e) => {
              e.stopPropagation()
              setDeleteTarget(s)
            }}
            className="text-destructive hover:bg-destructive/10" />
        </RowActions>
      )
    },
  }), [toggleSchedule])

  // Replace base name column with name+version combined column
  const columns = useMemo(
    () => [nameVersionColumn, ...scheduleColumns.slice(1), actionColumn],
    [nameVersionColumn, actionColumn],
  )

  return (
    <>
    <DataBodyTemplate
      title="Schedules"
      description="Manage cron and one-time pipeline schedules."
    >
      <DataBodyTemplate.Body>
        <DataBodyTemplate.Resource
          toolbarLeft={
            <div className="w-48">
              <FilterInput
                config={{
                  key: 'scheduleSearch',
                  type: 'text',
                  placeholder: 'Search schedules…',
                  display: { size: 'sm', leadingIcon: <Search /> },
                }}
                value={nameFilter}
                onChange={v => setNameFilter(typeof v === 'string' ? v : '')}
              />
            </div>
          }
          toolbarRight={
            <Button size="sm" onClick={() => navigate(`/projects/${projectId}/schedules/create`)}>
              <Plus size={14} className="mr-1.5" /> Create
            </Button>
          }
          notice={schedulesQuery.isError && (
            <QueryErrorNotice
              message="Failed to load schedules"
              error={schedulesQuery.error}
              onRetry={() => void schedulesQuery.refetch()}
            />
          )}
        >
          <DataGrid
            data={filteredSchedules}
            columns={columns}
            emptyMessage={schedulesQuery.isError ? undefined : 'No schedules yet. Create one to start.'}
            tableWidthMode="fill-last"
            rowHeight={44}
            rowCursor
            onRowClick={(row) => open(<ScheduleDetailPanel id={row.id} />, { size: 560 })}
            classNames={{ footer: 'pt-3' }}
            pagination={{
              pageSize: PAGE_SIZE,
              pageIndex,
              pageCount: Math.max(1, Math.ceil(total / PAGE_SIZE)),
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
          <AlertDialogTitle>Delete this schedule?</AlertDialogTitle>
          <AlertDialogDescription>
            "{deleteTarget?.name}" will be permanently deleted.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={deleting}
            onClick={() => {
              if (!deleteTarget) return
              deleteSchedule(deleteTarget.id)
              setDeleteTarget(null)
            }}
          >
            {deleting ? 'Deleting…' : 'Delete schedule'}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
    </>
  )
}

export default function WorkflowsPage() {
  return (
    <SidePanelProvider defaultSize={560} defaultMinSize={420} defaultMaxSize={1000}>
      <WorkflowsPageInner />
    </SidePanelProvider>
  )
}
