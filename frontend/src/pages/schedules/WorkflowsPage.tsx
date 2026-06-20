import { useNavigate } from 'react-router-dom'
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
import type { DataGridColumnDef } from '@loykin/gridkit'
import type { Schedule } from '@/features/schedules/api'

function WorkflowsPageInner() {
  const navigate = useNavigate()
  const projectId = useProjectId()
  const { open } = useSidePanel()
  const { data: schedules = [], isLoading, isError } = useSchedules()
  const { mutate: deleteSchedule } = useDeleteSchedule()
  const { mutate: toggleSchedule } = useToggleSchedule()

  const actionColumn: DataGridColumnDef<Schedule> = {
    id: 'actions',
    header: '',
    meta: { minWidth: 120 },
    cell: ({ row }) => {
      const s = row.original
      return (
        <div className="flex items-center gap-0.5">
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
        </div>
      )
    },
  }

  const columns = [...scheduleColumns, actionColumn]

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
