import { Link, useNavigate } from '@/lib/router'
import { Power, Trash2, X } from 'lucide-react'
import { DataGrid, DataGridPaginationCompact, type DataGridColumnDef } from '@loykin/gridkit'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import StatusBadge from '@/shared/components/StatusBadge'
import { useSchedule, useScheduleRuns, useDeleteSchedule, useToggleSchedule } from '@/features/schedules/hooks'
import { usePipeline } from '@/features/pipelines/hooks'
import { useProjectId } from '@/lib/projectContext'
import type { Run } from '@/features/runs/api'

const TYPE_LABEL: Record<string, string> = {
  immediate: 'Immediate',
  once: 'Once',
  cron: 'Cron',
}

const runColumns: DataGridColumnDef<Run>[] = [
  {
    id: 'id',
    header: 'Run ID',
    meta: { minWidth: 180, flex: 1 },
    cell: ({ row }) => (
      <span className="font-mono text-xs text-primary">{row.original.id.slice(0, 20)}…</span>
    ),
  },
  {
    id: 'status',
    header: 'Status',
    meta: { minWidth: 100 },
    cell: ({ row }) => <StatusBadge status={row.original.status} />,
  },
  {
    id: 'started_at',
    header: 'Started',
    meta: { minWidth: 140 },
    cell: ({ row }) => (
      <span className="text-xs text-muted-foreground">
        {new Date(row.original.started_at).toLocaleString()}
      </span>
    ),
  },
]

export function ScheduleDetailPanel({ id }: { id: string }) {
  const { close } = useSidePanel()
  const navigate = useNavigate()
  const projectId = useProjectId()
  const { data: schedule, isLoading: scheduleLoading } = useSchedule(id)
  const { data: runs = [], isLoading: runsLoading } = useScheduleRuns(id)
  const { data: templateVersion } = usePipeline(schedule?.template_version_id ?? '')
  const { mutate: deleteSchedule } = useDeleteSchedule()
  const { mutate: toggleSchedule } = useToggleSchedule()

  const closeBtn = (
    <Button variant="ghost" size="icon-sm" onClick={() => void close()}>
      <X className="h-3.5 w-3.5" />
    </Button>
  )

  if (scheduleLoading || runsLoading) {
    return (
      <PanelTemplate title="Loading…" actions={closeBtn}>
        <PanelTemplate.Section>
          <p className="text-xs text-muted-foreground">Loading…</p>
        </PanelTemplate.Section>
      </PanelTemplate>
    )
  }

  if (!schedule) {
    return (
      <PanelTemplate title="Not Found" actions={closeBtn}>
        <PanelTemplate.Section>
          <p className="text-xs text-muted-foreground">Schedule not found.</p>
        </PanelTemplate.Section>
      </PanelTemplate>
    )
  }

  const isCron = schedule.schedule_type === 'cron'

  return (
    <PanelTemplate
      eyebrow={TYPE_LABEL[schedule.schedule_type] ?? schedule.schedule_type}
      title={schedule.name}
      actions={
        <div className="flex items-center gap-1">
          {isCron && (
            <IconButton icon={<Power />} label={schedule.enabled ? 'Disable' : 'Enable'}
              onClick={() => toggleSchedule({ id: schedule.id, enabled: !schedule.enabled })}
              className={schedule.enabled ? 'text-primary hover:bg-primary/10' : ''} />
          )}
          <Badge variant="outline" className="text-[10px]">
            {TYPE_LABEL[schedule.schedule_type] ?? schedule.schedule_type}
          </Badge>
          <IconButton icon={<Trash2 />} label="Delete"
            onClick={() => {
              if (!confirm(`Delete schedule "${schedule.name}"?`)) return
              deleteSchedule(schedule.id, { onSuccess: () => void close() })
            }}
            className="text-destructive hover:bg-destructive/10" />
          {closeBtn}
        </div>
      }
    >
      <PanelTemplate.Section title="Details">
        <dl className="grid grid-cols-2 gap-3">
          {isCron && (
            <div>
              <dt className="text-xs text-muted-foreground">Cron Expression</dt>
              <dd className="mt-0.5 font-mono text-xs">{schedule.cron_expr || '—'}</dd>
            </div>
          )}
          {schedule.schedule_type === 'once' && (
            <div>
              <dt className="text-xs text-muted-foreground">Scheduled At</dt>
              <dd className="mt-0.5 text-xs">{new Date(schedule.next_run_at).toLocaleString()}</dd>
            </div>
          )}
          <div>
            <dt className="text-xs text-muted-foreground">Status</dt>
            <dd className="mt-0.5 text-xs">
              {schedule.enabled ? (isCron ? 'Active' : 'Waiting') : (schedule.schedule_type === 'cron' ? 'Disabled' : 'Done')}
            </dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Last Run</dt>
            <dd className="mt-0.5 text-xs">
              {schedule.last_run_at ? new Date(schedule.last_run_at).toLocaleString() : '—'}
            </dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Created</dt>
            <dd className="mt-0.5 text-xs">{new Date(schedule.created_at).toLocaleString()}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Total Runs</dt>
            <dd className="mt-0.5 text-xs font-semibold">{runs.length}</dd>
          </div>
          {templateVersion && (
            <div>
              <dt className="text-xs text-muted-foreground">Pipeline Version</dt>
              <dd className="mt-0.5 text-xs font-semibold">v{templateVersion.version}</dd>
            </div>
          )}
        </dl>
      </PanelTemplate.Section>

      {(templateVersion?.description || (templateVersion?.tags && templateVersion.tags.length > 0)) && (
        <PanelTemplate.Section title="Pipeline">
          {templateVersion.description && (
            <p className="mb-2 text-xs text-muted-foreground">{templateVersion.description}</p>
          )}
          {templateVersion.tags && templateVersion.tags.length > 0 && (
            <div className="flex flex-wrap gap-1">
              {templateVersion.tags.map(tag => (
                <Badge key={tag} variant="secondary" className="text-[10px]">{tag}</Badge>
              ))}
            </div>
          )}
        </PanelTemplate.Section>
      )}

      <PanelTemplate.Section title="Run History">
        {runs.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            {schedule.schedule_type === 'immediate' ? 'Running…' : 'No runs yet.'}
          </p>
        ) : (
          <DataGrid
            data={runs}
            columns={runColumns}
            tableWidthMode="fill-last"
            rowHeight={40}
            rowCursor
            onRowClick={() => {
              void close()
              navigate(`/projects/${projectId}/history`)
            }}
            pagination={{ pageSize: 5 }}
            footer={(table) => (
              <div className="flex h-8 items-center justify-between px-1 text-xs text-muted-foreground">
                <Link
                  to={`/projects/${projectId}/history`}
                  className="text-primary hover:underline"
                  onClick={() => void close()}
                >
                  View all in History →
                </Link>
                <DataGridPaginationCompact table={table} />
              </div>
            )}
          />
        )}
      </PanelTemplate.Section>

      <PanelTemplate.Section title="Pipeline YAML">
        <pre className="overflow-x-auto rounded border border-border bg-muted/30 p-2 text-xs leading-6 text-muted-foreground">
          {schedule.pipeline_yaml || '(empty)'}
        </pre>
      </PanelTemplate.Section>
    </PanelTemplate>
  )
}
