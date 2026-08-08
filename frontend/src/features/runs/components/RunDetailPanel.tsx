import { useState } from 'react'
import { RotateCcw, RefreshCw, XCircle, Trash2, X } from 'lucide-react'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { Link } from '@/lib/router'
import { useProjectId } from '@/lib/projectContext'
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
import { useRun, useRunSteps, useDeleteRun, useCancelRun, useRerunRun } from '@/features/runs/hooks'
import StatusBadge from '@/shared/components/StatusBadge'

export function RunDetailPanel({ id }: { id: string }) {
  const { close, open } = useSidePanel()
  const projectId = useProjectId()

  const { data: run = null, isLoading } = useRun(id)
  const { data: steps = [] } = useRunSteps(id)

  const { mutate: deleteRun, isPending: deleting } = useDeleteRun()
  const { mutate: cancelRun, isPending: cancelling } = useCancelRun()
  const { mutateAsync: rerunRun } = useRerunRun()
  const [confirmAction, setConfirmAction] = useState<'cancel' | 'delete' | null>(null)

  const closeBtn = (
    <Button variant="ghost" size="icon-sm" onClick={() => void close()}>
      <X className="h-3.5 w-3.5" />
    </Button>
  )

  if (isLoading || !run) {
    return (
      <PanelTemplate title="Loading…" actions={closeBtn}>
        <PanelTemplate.Section>
          <p className="text-xs text-muted-foreground">Loading…</p>
        </PanelTemplate.Section>
      </PanelTemplate>
    )
  }

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
            onClick={() => setConfirmAction('cancel')}
            className="text-orange-400 hover:bg-orange-950" />
          <IconButton icon={<RotateCcw />} label="Rerun"
            disabled={run.status === 'running' || run.status === 'scheduled'}
            onClick={rerun}
            className="text-indigo-400 hover:bg-indigo-950" />
          <IconButton icon={<RefreshCw />} label="Retry Failed"
            disabled={run.status !== 'failed'}
            onClick={rerun}
            className="text-yellow-400 hover:bg-yellow-950" />
          <IconButton icon={<Trash2 />} label="Delete"
            disabled={run.status === 'running'}
            onClick={() => setConfirmAction('delete')}
            className="text-destructive hover:bg-destructive/10" />
          {closeBtn}
        </div>
      }
    >
      <PanelTemplate.Section title="Details">
        <dl className="grid grid-cols-2 gap-3">
          <div>
            <dt className="text-xs text-muted-foreground">Started</dt>
            <dd className="mt-0.5 text-xs">{new Date(run.started_at).toLocaleString()}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Ended</dt>
            <dd className="mt-0.5 text-xs">{run.ended_at ? new Date(run.ended_at).toLocaleString() : '—'}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Steps</dt>
            <dd className="mt-0.5 text-xs">{completedSteps} / {steps.length} completed</dd>
          </div>
          {run.schedule_id && (
            <div>
              <dt className="text-xs text-muted-foreground">Schedule</dt>
              <dd className="mt-0.5 font-mono text-xs">{run.schedule_id.slice(0, 12)}…</dd>
            </div>
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

    <AlertDialog open={confirmAction != null} onOpenChange={open => { if (!open) setConfirmAction(null) }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {confirmAction === 'cancel' ? 'Cancel this run?' : 'Delete this run?'}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {confirmAction === 'cancel'
              ? `Run ${run.id} will be stopped immediately.`
              : `Run ${run.id} and its artifacts will be permanently removed.`}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Back</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={confirmAction === 'cancel' ? cancelling : deleting}
            onClick={() => {
              if (confirmAction === 'cancel') {
                cancelRun(run.id)
              } else if (confirmAction === 'delete') {
                deleteRun(run.id, { onSuccess: () => void close() })
              }
              setConfirmAction(null)
            }}
          >
            {confirmAction === 'cancel'
              ? (cancelling ? 'Cancelling…' : 'Cancel run')
              : (deleting ? 'Deleting…' : 'Delete run')}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
    </>
  )
}
