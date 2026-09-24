import { useNavigate, useParams } from '@/lib/router'
import { useMemo } from 'react'
import { useProjectId } from '@/features/projects/context'
import { Power, Trash2 } from 'lucide-react'
import { DataGrid, DataGridPaginationCompact } from '@loykin/gridkit'
import { DetailBodyTemplate, PageTopBar } from '@loykin/designkit'
import { IconButton } from '@/components/ui/icon-button'
import { Badge } from '@/components/ui/badge'
import RunDAG from '@/features/runs/components/RunDAG'
import { useSchedule, useScheduleRuns, useDeleteSchedule, useToggleSchedule } from '@/features/schedules/hooks'
import { makeScheduleRunColumns } from '@/features/schedules/columns'
import type { Schedule } from '@/features/schedules/types'
import { useDeleteTarget } from '@/shared/hooks/useDeleteTarget'
import { fmtDate } from '@/lib/format'
import { PageCrumbs } from '@/shared/components/PageCrumbs'
import { ConfirmDialog } from '@/shared/components/ConfirmDialog'

const TYPE_LABEL: Record<string, string> = {
  immediate: 'Immediate',
  once: 'Once',
  cron: 'Cron',
}

export default function ScheduleDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const projectId = useProjectId()
  const { data: schedule, isLoading: scheduleLoading } = useSchedule(id!)
  const { data: runs = [], isLoading: runsLoading } = useScheduleRuns(id!)
  const { mutateAsync: deleteSchedule, isPending: deleting } = useDeleteSchedule()
  const { mutate: toggleSchedule } = useToggleSchedule()
  const runColumns = useMemo(() => makeScheduleRunColumns(projectId), [projectId])
  const deleteTarget = useDeleteTarget<Schedule>()

  if (!scheduleLoading && !schedule) {
    return (
      <DetailBodyTemplate topBar={<PageTopBar left={<PageCrumbs items={['Pipelines', { label: 'Schedules', to: `/projects/${projectId}/schedules` }, id ?? '']} />} />} title="Not Found">
        <DetailBodyTemplate.Section>
          <p className="text-sm text-muted-foreground">Schedule not found.</p>
        </DetailBodyTemplate.Section>
      </DetailBodyTemplate>
    )
  }

  const isCron = schedule?.schedule_type === 'cron'

  return (
    <>
    <DetailBodyTemplate
      topBar={<PageTopBar left={<PageCrumbs items={['Pipelines', { label: 'Schedules', to: `/projects/${projectId}/schedules` }, schedule?.name ?? id ?? '']} />} />}
      title={schedule?.name ?? '…'}
      description={schedule ? `${TYPE_LABEL[schedule.schedule_type] ?? schedule.schedule_type} schedule` : ''}
      actions={schedule && (
        <div className="flex items-center gap-0.5">
          {isCron && (
            <IconButton icon={<Power />} label={schedule.enabled ? 'Disable' : 'Enable'}
              onClick={() => toggleSchedule({ id: schedule.id, enabled: !schedule.enabled })}
              className={schedule.enabled ? 'text-primary hover:bg-primary/10' : ''} />
          )}
          <Badge variant="outline">{TYPE_LABEL[schedule.schedule_type] ?? schedule.schedule_type}</Badge>
          <IconButton icon={<Trash2 />} label="Delete"
            onClick={() => deleteTarget.requestDelete(schedule)}
            className="text-destructive hover:bg-destructive/10" />
        </div>
      )}
    >
      <DetailBodyTemplate.Section title="Details" surface="bordered">
        {scheduleLoading ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : schedule ? (
          <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            {isCron && (
              <div>
                <dt className="text-xs text-muted-foreground">Cron Expression</dt>
                <dd className="mt-1 font-mono text-sm">{schedule.cron_expr || '-'}</dd>
              </div>
            )}
            {schedule.schedule_type === 'once' && (
              <div>
                <dt className="text-xs text-muted-foreground">Scheduled At</dt>
                <dd className="mt-1 text-sm">{fmtDate(schedule.next_run_at)}</dd>
              </div>
            )}
            <div>
              <dt className="text-xs text-muted-foreground">Status</dt>
              <dd className="mt-1 text-sm">
                {schedule.enabled ? (isCron ? 'Active' : 'Waiting') : (schedule.schedule_type === 'cron' ? 'Disabled' : 'Done')}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-muted-foreground">Last Run</dt>
              <dd className="mt-1 text-sm">
                {schedule.last_run_at ? fmtDate(schedule.last_run_at) : '-'}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-muted-foreground">Retention</dt>
              <dd className="mt-1 text-sm">
                {schedule.max_runs > 0 ? `Keep last ${schedule.max_runs}` : 'Unlimited'}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-muted-foreground">Created</dt>
              <dd className="mt-1 text-sm">{fmtDate(schedule.created_at)}</dd>
            </div>
            <div>
              <dt className="text-xs text-muted-foreground">Total Runs</dt>
              <dd className="mt-1 text-sm font-semibold">{runs.length}</dd>
            </div>
          </dl>
        ) : null}
      </DetailBodyTemplate.Section>

      <DetailBodyTemplate.Section surface="bordered">
        <RunDAG
          pipelineYaml={schedule?.pipeline_yaml ?? ''}
          steps={[]}
          selected={null}
          onSelectStep={() => {}}
        />
      </DetailBodyTemplate.Section>

      <DetailBodyTemplate.Section title="Run History" surface="plain">
        {runsLoading ? (
          <p className="py-4 text-sm text-muted-foreground">Loading…</p>
        ) : runs.length === 0 ? (
          <p className="py-4 text-sm text-muted-foreground">
            {schedule?.schedule_type === 'immediate' ? 'Running...' : 'No runs yet.'}
          </p>
        ) : (
          <DataGrid
            data={runs}
            columns={runColumns}
            tableWidthMode="fill-last"
            rowHeight={44}
            pagination={{ pageSize: 10 }}
            footer={(table) => (
              <div className="flex h-9 items-center justify-between px-1 text-xs text-muted-foreground">
                <span>{runs.length} results</span>
                <DataGridPaginationCompact table={table} />
              </div>
            )}
          />
        )}
      </DetailBodyTemplate.Section>

      <DetailBodyTemplate.Section title="Pipeline YAML" surface="bordered">
        <pre className="overflow-x-auto text-xs leading-6 text-muted-foreground">{schedule?.pipeline_yaml || '(empty)'}</pre>
      </DetailBodyTemplate.Section>
    </DetailBodyTemplate>

    <ConfirmDialog
      open={deleteTarget.open}
      onCancel={deleteTarget.cancel}
      title="Delete this schedule?"
      description={`"${deleteTarget.target?.name}" will be permanently deleted.`}
      error={deleteTarget.error}
      pending={deleting}
      confirmLabel={deleting ? 'Deleting…' : 'Delete schedule'}
      onConfirm={() => void deleteTarget.confirm(async target => {
        await deleteSchedule(target.id)
        navigate(`/projects/${projectId}/schedules`)
      })}
    />
    </>
  )
}
