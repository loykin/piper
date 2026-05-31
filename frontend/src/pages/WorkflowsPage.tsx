import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { DataGrid, DataGridPaginationCompact } from '@loykin/gridkit'
import { DataPage } from '@loykin/designkit'
import { listSchedules, setScheduleEnabled, deleteSchedule, type Schedule } from '@/features/schedules/api'
import { scheduleColumns } from '@/features/schedules/columns'
import type { DataGridColumnDef } from '@loykin/gridkit'

export default function WorkflowsPage() {
  const [schedules, setSchedules] = useState<Schedule[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const navigate = useNavigate()
  const intervalRef = useRef<ReturnType<typeof setInterval>>(0 as unknown as ReturnType<typeof setInterval>)

  const load = () =>
    listSchedules()
      .then((data) => { setSchedules(data); setLoadError('') })
      .catch(() => setLoadError('Failed to load schedules.'))
      .finally(() => setLoading(false))

  useEffect(() => {
    load()
    intervalRef.current = setInterval(load, 5000)
    return () => clearInterval(intervalRef.current)
  }, [])

  const actionColumn: DataGridColumnDef<Schedule> = {
    id: 'actions',
    header: '',
    meta: { minWidth: 220 },
    cell: ({ row }) => {
      const s = row.original
      return (
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => navigate(`/schedules/${s.id}`)}
            className="rounded border border-border px-2 py-1 text-xs text-foreground hover:bg-accent"
          >
            View
          </button>
          {s.schedule_type === 'cron' && (
            <button
              type="button"
              onClick={async (e) => {
                e.stopPropagation()
                try { await setScheduleEnabled(s.id, !s.enabled); await load() } catch { /* no-op */ }
              }}
              className={`rounded border px-2 py-1 text-xs ${s.enabled ? 'border-primary/40 bg-primary/10 text-primary' : 'border-border bg-secondary text-muted-foreground'}`}
            >
              {s.enabled ? 'Enabled' : 'Disabled'}
            </button>
          )}
          <button
            type="button"
            onClick={async (e) => {
              e.stopPropagation()
              if (!confirm(`Delete schedule "${s.name}"?`)) return
              try { await deleteSchedule(s.id); await load() } catch { /* no-op */ }
            }}
            className="rounded border border-destructive/40 px-2 py-1 text-xs text-destructive hover:bg-destructive/10"
          >
            Delete
          </button>
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
          <button
            type="button"
            onClick={() => navigate('/schedules/create')}
            className="rounded-md bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground hover:opacity-90"
          >
            + Create
          </button>
        </DataPage.Actions>
      </DataPage.Header>
      <DataPage.Content>
        {loadError && (
          <div className="mb-[var(--dk-page-padding-y)] rounded border border-destructive/30 bg-destructive/10 px-4 py-2 text-xs text-destructive">
            {loadError}
          </div>
        )}
        {loading ? (
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
