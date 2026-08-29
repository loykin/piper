import { useEffect, useState } from 'react'
import { useParams, Link, useNavigate } from '@/lib/router'
import { useProjectId } from '@/lib/projectContext'
import { RotateCcw, RefreshCw, XCircle, Trash2 } from 'lucide-react'
import { DetailBodyTemplate } from '@loykin/designkit'
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
import { IconButton } from '@/components/ui/icon-button'
import { useRun, useRunSteps, useDeleteRun, useCancelRun, useRerunRun, useRetryStep, useStepArtifacts } from '@/features/runs/hooks'
import StatusBadge from '@/shared/components/StatusBadge'
import RunDAG from '@/shared/components/RunDAG'
import { StepList } from '@/features/runs/components/StepList'
import { LogViewer } from '@/features/runs/components/LogViewer'
import { ArtifactPanel } from '@/features/runs/components/ArtifactPanel'
import { MLflowRunLinks } from '@/features/mlflow/components/MLflowRunLinks'

export default function RunDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const projectId = useProjectId()
  const [selectedStep, setSelectedStep] = useState<string | null>(null)
  const [confirmAction, setConfirmAction] = useState<'cancel' | 'delete' | null>(null)

  const { data: run = null, isLoading, isError } = useRun(id!)
  const { data: steps = [] } = useRunSteps(id!)

  const { data: allArtifacts = [] } = useStepArtifacts(id!, selectedStep)

  const { mutate: deleteRun, isPending: deletingRun } = useDeleteRun()
  const { mutate: cancelRun, isPending: cancellingRun } = useCancelRun()
  const { mutate: rerunRun } = useRerunRun()
  const { mutate: retryStep } = useRetryStep()

  useEffect(() => {
    if (steps.length && !selectedStep) {
      setSelectedStep(steps[0].step_name)
    }
  }, [steps, selectedStep])

  if (isError || (!isLoading && !run)) {
    return (
      <DetailBodyTemplate
        eyebrow={<Link to={`/projects/${projectId}/history`} className="hover:text-foreground transition-colors">← History</Link>}
        title="Run not found"
      >
        <DetailBodyTemplate.Section>
          <p className="text-sm text-muted-foreground">
            Run <span className="font-mono">{id}</span> doesn't exist or may have been deleted.
          </p>
        </DetailBodyTemplate.Section>
      </DetailBodyTemplate>
    )
  }

  if (isLoading || !run) {
    return (
      <DetailBodyTemplate title="Loading…">
        <DetailBodyTemplate.Section>
          <p className="text-sm text-muted-foreground">Loading…</p>
        </DetailBodyTemplate.Section>
      </DetailBodyTemplate>
    )
  }

  return (
    <>
    <DetailBodyTemplate
      eyebrow={<Link to={`/projects/${projectId}/history`} className="hover:text-foreground transition-colors">← History</Link>}
      title={<span className="font-mono">{run.id}</span>}
      status={<StatusBadge status={run.status} />}
      actions={
        <div className="flex items-center gap-0.5">
          <IconButton icon={<XCircle />} label="Cancel Run"
            disabled={run.status !== 'running' && run.status !== 'scheduled'}
            onClick={() => setConfirmAction('cancel')}
            className="text-orange-400 hover:bg-orange-950" />
          <IconButton icon={<RotateCcw />} label="Rerun"
            disabled={run.status === 'running' || run.status === 'scheduled'}
            onClick={() => rerunRun(run.id, { onSuccess: (data) => navigate(`/projects/${projectId}/runs/${data.run_id}`) })}
            className="text-indigo-400 hover:bg-indigo-950" />
          <IconButton icon={<RefreshCw />} label="Retry Failed"
            disabled={run.status !== 'failed'}
            onClick={() => rerunRun(run.id, { onSuccess: (data) => navigate(`/projects/${projectId}/runs/${data.run_id}`) })}
            className="text-yellow-400 hover:bg-yellow-950" />
          <IconButton icon={<Trash2 />} label="Delete Run"
            disabled={run.status === 'running'}
            onClick={() => setConfirmAction('delete')}
            className="text-destructive hover:bg-destructive/10" />
        </div>
      }
    >
      <DetailBodyTemplate.Section>
        <RunDAG
          pipelineYaml={run.pipeline_yaml}
          steps={steps}
          selected={selectedStep}
          onSelectStep={setSelectedStep}
        />
      </DetailBodyTemplate.Section>

      <DetailBodyTemplate.Section>
        <StepList
          steps={steps}
          selectedId={selectedStep}
          onSelect={setSelectedStep}
          onRetry={(stepName) => {
            retryStep({ runId: run.id, stepId: stepName }, {
              onSuccess: (data) => navigate(`/projects/${projectId}/runs/${data.run_id}`),
              onError: (err) => alert(err.message),
            })
          }}
        />
      </DetailBodyTemplate.Section>

      <DetailBodyTemplate.Section>
        <ArtifactPanel projectId={projectId} runId={id!} artifacts={allArtifacts} />
      </DetailBodyTemplate.Section>

      <DetailBodyTemplate.Section>
        <MLflowRunLinks runId={id!} />
      </DetailBodyTemplate.Section>

      <DetailBodyTemplate.Section>
        <LogViewer runId={id!} stepId={selectedStep} />
      </DetailBodyTemplate.Section>
    </DetailBodyTemplate>

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
            disabled={confirmAction === 'cancel' ? cancellingRun : deletingRun}
            onClick={() => {
              if (confirmAction === 'cancel') {
                cancelRun(run.id)
              } else if (confirmAction === 'delete') {
                deleteRun(run.id, { onSuccess: () => navigate(`/projects/${projectId}/history`) })
              }
              setConfirmAction(null)
            }}
          >
            {confirmAction === 'cancel'
              ? (cancellingRun ? 'Cancelling…' : 'Cancel run')
              : (deletingRun ? 'Deleting…' : 'Delete run')}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
    </>
  )
}
