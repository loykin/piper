import { useMemo } from 'react'
import { useNavigate } from '@/lib/router'
import { useProjectId } from '@/lib/projectContext'
import { Power, Plus, Trash2 } from 'lucide-react'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { DataGrid, DataGridPaginationCompact } from '@loykin/gridkit'
import { DataBodyTemplate } from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { scheduleColumns } from '@/features/schedules/columns'
import { ScheduleDetailPanel } from '@/features/schedules/components/ScheduleDetailPanel'
import { useSchedules, useDeleteSchedule, useToggleSchedule } from '@/features/schedules/hooks'
import { usePipelines } from '@/features/pipelines/hooks'
import { RowActions } from '@/shared/components/RowActions'
import type { DataGridColumnDef } from '@loykin/gridkit'
import type { Schedule } from '@/features/schedules/api'

function WorkflowsPageInner() {
  const navigate = useNavigate()
  const projectId = useProjectId()
  const { open } = useSidePanel()
  const { data: schedules = [], isLoading, isError } = useSchedules()
  const { data: pipelines = [] } = usePipelines()
  const { mutate: deleteSchedule } = useDeleteSchedule()
  const { mutate: toggleSchedule } = useToggleSchedule()

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
              if (!confirm(`Delete schedule "${s.name}"?`)) return
              deleteSchedule(s.id)
            }}
            className="text-destructive hover:bg-destructive/10" />
        </RowActions>
      )
    },
  }), [toggleSchedule, deleteSchedule])

  // Replace base name column with name+version combined column
  const columns = useMemo(
    () => [nameVersionColumn, ...scheduleColumns.slice(1), actionColumn],
    [nameVersionColumn, actionColumn],
  )

  return (
    <DataBodyTemplate
      title="Schedules"
      description="Manage cron and one-time pipeline schedules."
      actions={
        <Button size="sm" onClick={() => navigate(`/projects/${projectId}/schedules/create`)}>
          <Plus size={14} className="mr-1.5" /> Create
        </Button>
      }
    >
      <DataBodyTemplate.Body>
        {isError && (
          <div className="mb-4 rounded border border-destructive/30 bg-destructive/10 px-4 py-2 text-xs text-destructive">
            Failed to load schedules.
          </div>
        )}
        <DataGrid
          data={schedules}
          columns={columns}
          isLoading={isLoading}
          emptyMessage="No schedules yet. Create one to start."
          tableWidthMode="fill-last"
          rowHeight={44}
          rowCursor
          onRowClick={(row) => open(<ScheduleDetailPanel id={row.id} />, { size: 560 })}
          pagination={{ pageSize: 20 }}
          footer={(table) => (
            <div className="flex h-9 items-center justify-between px-1 text-xs text-muted-foreground">
              <span>{schedules.length} results</span>
              <DataGridPaginationCompact table={table} />
            </div>
          )}
        />
      </DataBodyTemplate.Body>
    </DataBodyTemplate>
  )
}

export default function WorkflowsPage() {
  return (
    <SidePanelProvider defaultSize={560} defaultMinSize={420} defaultMaxSize={1000}>
      <WorkflowsPageInner />
    </SidePanelProvider>
  )
}
