import { useNavigate } from 'react-router-dom'
import { ArrowRight, Power, Plus, Trash2 } from 'lucide-react'
import { DataGrid, DataGridPaginationCompact } from '@loykin/gridkit'
import { DataPage } from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { scheduleColumns } from '@/features/schedules/columns'
import { useSchedules, useDeleteSchedule, useToggleSchedule } from '@/features/schedules/hooks'
import type { DataGridColumnDef } from '@loykin/gridkit'
import type { Schedule } from '@/features/schedules/api'

export default function WorkflowsPage() {
  const navigate = useNavigate()
  const { data: schedules = [], isLoading, isError } = useSchedules()
  const { mutate: deleteSchedule } = useDeleteSchedule()
  const { mutate: toggleSchedule } = useToggleSchedule()

  const actionColumn: DataGridColumnDef<Schedule> = {
    id: 'actions',
    header: '',
    meta: { minWidth: 220 },
    cell: ({ row }) => {
      const s = row.original
      return (
        <div className="flex items-center gap-0.5">
          <IconButton icon={<ArrowRight />} label="View"
            onClick={() => navigate(`/schedules/${s.id}`)} />
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
    <DataPage>
      <DataPage.Header>
        <DataPage.TitleBlock
          title="Schedules"
          description="Manage cron and one-time pipeline schedules."
        />
        <DataPage.Actions>
          <Button size="sm" onClick={() => navigate('/schedules/create')}>
            <Plus size={14} className="mr-1.5" /> Create
          </Button>
        </DataPage.Actions>
      </DataPage.Header>
      <DataPage.Content>
        {isError && (
          <div className="mb-[var(--dk-page-padding-y)] rounded border border-destructive/30 bg-destructive/10 px-4 py-2 text-xs text-destructive">
            Failed to load schedules.
          </div>
        )}
        {isLoading ? (
          <div className="py-8 text-sm text-muted-foreground">Loading...</div>
        ) : schedules.length === 0 ? (
          <div className="py-8 text-sm text-muted-foreground">No schedules yet. Create one to start.</div>
        ) : (
          <DataPage.Group surface="none" className="h-full">
            <DataPage.GroupBody className="h-full [&_.dg-shell]:h-full [&_.dg-table-wrapper]:min-h-0 [&_.dg-table-wrapper]:flex-1">
              <DataGrid
                data={schedules}
                columns={columns}
                tableWidthMode="fill-last"
                rowHeight={44}
                rowCursor
                onRowClick={(row) => navigate(`/schedules/${row.id}`)}
                pagination={{ pageSize: 20 }}
                footer={(table) => (
                  <div className="flex h-9 items-center justify-between px-1 text-xs text-muted-foreground">
                    <span>{schedules.length} results</span>
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
