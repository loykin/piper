import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { DataGrid, DataGridPaginationCompact, type DataGridColumnDef } from '@loykin/gridkit'
import { DataPage } from '@loykin/designkit'
import { Badge } from '@/components/ui/badge'
import { listSchedules, setScheduleEnabled, deleteSchedule, type Schedule } from '../api'

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

  const columns: DataGridColumnDef<Schedule>[] = [
    {
      accessorKey: 'name',
      header: 'Name',
      meta: { minWidth: 200, flex: 1 },
    },
    {
      id: 'type',
      header: 'Type',
      meta: { minWidth: 100 },
      cell: ({ row }) => (
        <Badge variant={row.original.schedule_type === 'cron' ? 'default' : 'secondary'}>
          {row.original.schedule_type === 'cron' ? 'Cron' : 'Once'}
        </Badge>
      ),
    },
    {
      id: 'schedule',
      header: 'Schedule',
      meta: { minWidth: 200 },
      cell: ({ row }) => (
        <span className="font-mono text-xs text-muted-foreground">
          {row.original.schedule_type === 'cron'
            ? row.original.cron_expr || '-'
            : new Date(row.original.next_run_at).toLocaleString()}
        </span>
      ),
    },
    {
      id: 'next_run_at',
      header: 'Next Run',
      meta: { minWidth: 180 },
      cell: ({ row }) => (
        <span className="text-xs text-muted-foreground">
          {row.original.schedule_type === 'cron'
            ? new Date(row.original.next_run_at).toLocaleString()
            : row.original.enabled ? new Date(row.original.next_run_at).toLocaleString() : 'Done'}
        </span>
      ),
    },
    {
      id: 'last_run_at',
      header: 'Last Run',
      meta: { minWidth: 180 },
      cell: ({ row }) => (
        <span className="text-xs text-muted-foreground">
          {row.original.last_run_at ? new Date(row.original.last_run_at).toLocaleString() : '-'}
        </span>
      ),
    },
    {
      id: 'created_at',
      header: 'Created',
      meta: { minWidth: 180 },
      cell: ({ row }) => (
        <span className="text-xs text-muted-foreground">{new Date(row.original.created_at).toLocaleString()}</span>
      ),
    },
    {
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
    },
  ]

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
