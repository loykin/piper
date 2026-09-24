import { useEffect, useState } from 'react'
import { useParams, useNavigate } from '@/lib/router'
import { useProjectId } from '@/features/projects/context'
import { RotateCcw, RefreshCw, XCircle } from 'lucide-react'
import { DetailBodyTemplate, PageTopBar } from '@loykin/designkit'
import { IconButton } from '@/components/ui/icon-button'
import { useRun, useRunSteps, useCancelRun, useRerunRun, useRetryStep, useStepArtifacts } from '@/features/runs/hooks'
import StatusBadge from '@/shared/components/StatusBadge'
import RunDAG from '@/features/runs/components/RunDAG'
import { StepList } from '@/features/runs/components/StepList'
import { LogViewer } from '@/features/runs/components/LogViewer'
import { ArtifactPanel } from '@/features/runs/components/ArtifactPanel'
import { MLflowRunLinks } from '@/features/mlflow/components/MLflowRunLinks'
import { RunActionConfirmDialog, type RunConfirmVerb } from '@/features/runs/components/RunActionConfirmDialog'
import { useConfirmAction } from '@/shared/hooks/useConfirmAction'
import { toneAction } from '@/shared/status'
import { PageCrumbs } from '@/shared/components/PageCrumbs'

export default function RunDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const projectId = useProjectId()
  const [selectedStep, setSelectedStep] = useState<string | null>(null)
  const { action: confirmAction, requestAction: requestConfirm, cancel: cancelConfirm } = useConfirmAction<RunConfirmVerb>()

  const { data: run = null, isLoading, isError } = useRun(id!)
  const { data: steps = [] } = useRunSteps(id!)

  const { data: allArtifacts = [] } = useStepArtifacts(id!, selectedStep)

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
        topBar={<PageTopBar left={<PageCrumbs items={['Pipelines', { label: 'Run History', to: `/projects/${projectId}/history` }, id ?? '']} />} />}
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
      <DetailBodyTemplate topBar={<PageTopBar left={<PageCrumbs items={['Pipelines', { label: 'Run History', to: `/projects/${projectId}/history` }, id ?? '']} />} />} title="Loading…">
        <DetailBodyTemplate.Section>
          <p className="text-sm text-muted-foreground">Loading…</p>
        </DetailBodyTemplate.Section>
      </DetailBodyTemplate>
    )
  }

  return (
    <>
    <DetailBodyTemplate
      topBar={<PageTopBar left={<PageCrumbs items={['Pipelines', { label: 'Run History', to: `/projects/${projectId}/history` }, id ?? '']} />} />}
      title={<span className="font-mono">{run.id}</span>}
      status={<StatusBadge status={run.status} />}
      actions={
        <div className="flex items-center gap-0.5">
          <IconButton icon={<XCircle />} label="Cancel Run"
            disabled={run.status !== 'running' && run.status !== 'scheduled'}
            onClick={() => requestConfirm('cancel')}
            className={toneAction.attention} />
          <IconButton icon={<RotateCcw />} label="Rerun"
            disabled={run.status === 'running' || run.status === 'scheduled'}
            onClick={() => rerunRun(run.id, { onSuccess: (data) => navigate(`/projects/${projectId}/runs/${data.run_id}`) })}
            className={toneAction.accent} />
          <IconButton icon={<RefreshCw />} label="Retry Failed"
            disabled={run.status !== 'failed'}
            onClick={() => rerunRun(run.id, { onSuccess: (data) => navigate(`/projects/${projectId}/runs/${data.run_id}`) })}
            className={toneAction.warning} />
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

    <RunActionConfirmDialog
      runId={run.id}
      action={confirmAction}
      onDismiss={cancelConfirm}
      cancelling={cancellingRun}
      onConfirmCancel={() => cancelRun(run.id)}
    />
    </>
  )
}
