import { RotateCcw, XCircle } from 'lucide-react'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { Link } from '@/lib/router'
import { useProjectId } from '@/features/projects/context'
import { IconButton } from '@/components/ui/icon-button'
import { useRun, useRunSteps, useCancelRun, useRerunRun } from '@/features/runs/hooks'
import StatusBadge from '@/shared/components/StatusBadge'
import { RunActionConfirmDialog, type RunConfirmVerb } from '@/features/runs/components/RunActionConfirmDialog'
import { useConfirmAction } from '@/shared/hooks/useConfirmAction'
import { toneAction } from '@/shared/status'
import { fmtDate } from '@/lib/format'
import { PanelCloseButton, PanelPlaceholder } from '@/shared/components/PanelPlaceholder'

export function RunDetailPanel({ id }: { id: string }) {
  const { close, open } = useSidePanel()
  const projectId = useProjectId()

  const query = useRun(id)
  const run = query.data ?? null
  const { data: steps = [] } = useRunSteps(id)

  const { mutateAsync: cancelRun, isPending: cancelling } = useCancelRun()
  const { mutateAsync: rerunRun } = useRerunRun()
  const confirmation = useConfirmAction<RunConfirmVerb>()


  if (!run) return <PanelPlaceholder query={query} noun="run" />

  const failedSteps = steps.filter(s => s.status === 'failed' && s.error)
  const completedSteps = steps.filter(s => s.status === 'done').length

  function rerun() {
    void rerunRun(run!.id).then((data) => {
      void close()
      open(<RunDetailPanel id={data.run_id} />, { size: 480 })
    })
  }

  return (
    <>
    <PanelTemplate
      eyebrow="Run"
      title={run.id}
      status={<StatusBadge status={run.status} />}
      actions={
        <div className="flex items-center gap-1">
          <IconButton icon={<XCircle />} label="Cancel"
            disabled={run.status !== 'running' && run.status !== 'scheduled'}
            onClick={() => confirmation.requestAction('cancel')}
            className={toneAction.attention} />
          <IconButton icon={<RotateCcw />} label="Rerun"
            disabled={run.status === 'running' || run.status === 'scheduled'}
            onClick={rerun}
            className={toneAction.accent} />
          <PanelCloseButton />
        </div>
      }
    >
      <PanelTemplate.Section title="Details">
        <dl className="space-y-2">
          <PanelTemplate.Row label="Started">{fmtDate(run.started_at)}</PanelTemplate.Row>
          <PanelTemplate.Row label="Ended">{run.ended_at ? fmtDate(run.ended_at) : '—'}</PanelTemplate.Row>
          <PanelTemplate.Row label="Steps">{completedSteps} / {steps.length} completed</PanelTemplate.Row>
          {run.schedule_id && (
            <PanelTemplate.Row label="Schedule">{run.schedule_id.slice(0, 12)}…</PanelTemplate.Row>
          )}
        </dl>
      </PanelTemplate.Section>

      {failedSteps.length > 0 && (
        <PanelTemplate.Section title="Failed Steps">
          <div className="space-y-1.5">
            {failedSteps.map(s => (
              <div key={s.step_name} className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2">
                <p className="text-xs font-medium text-destructive">{s.step_name}</p>
                <p className="mt-1 whitespace-pre-wrap break-all font-mono text-[11px] text-muted-foreground">{s.error}</p>
              </div>
            ))}
          </div>
        </PanelTemplate.Section>
      )}

      <PanelTemplate.Section>
        <Link
          to={`/projects/${projectId}/runs/${run.id}`}
          className="text-xs text-primary hover:underline"
          onClick={() => void close()}
        >
          View full run (DAG, logs, artifacts) →
        </Link>
      </PanelTemplate.Section>
    </PanelTemplate>

    <RunActionConfirmDialog
      runId={run.id}
      confirmation={confirmation}
      cancelling={cancelling}
      onConfirmCancel={() => cancelRun(run.id)}
    />
    </>
  )
}
