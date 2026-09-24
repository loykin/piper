import { useEffect, useState } from 'react'
import { useParams, useNavigate } from '@/lib/router'
import { useProjectId } from '@/features/projects/context'
import { RotateCcw, XCircle } from 'lucide-react'
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
import { MutationErrors } from '@/shared/components/MutationErrors'
import { hasMutationError } from '@/shared/mutationErrors'
import { PageState } from '@/shared/components/PageState'

export default function RunDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const projectId = useProjectId()
  const [selectedStep, setSelectedStep] = useState<string | null>(null)
  const confirmation = useConfirmAction<RunConfirmVerb>()

  const runQuery = useRun(id!)
  const run = runQuery.data ?? null
  const { data: steps = [] } = useRunSteps(id!)

  const { data: allArtifacts = [] } = useStepArtifacts(id!, selectedStep)

  const { mutateAsync: cancelRun, isPending: cancellingRun } = useCancelRun()
  const rerun = useRerunRun()
  const retry = useRetryStep()
  const { mutate: rerunRun } = rerun
  const { mutate: retryStep } = retry

  useEffect(() => {
    if (steps.length && !selectedStep) {
      setSelectedStep(steps[0].step_name)
    }
  }, [steps, selectedStep])

  if (!run) {
    return (
      <PageState
        query={runQuery}
        noun="run"
        topBar={<PageTopBar left={<PageCrumbs items={['Pipelines', { label: 'Run History', to: `/projects/${projectId}/history` }, id ?? '']} />} />}
      />
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
            onClick={() => confirmation.requestAction('cancel')}
            className={toneAction.attention} />
          <IconButton icon={<RotateCcw />} label="Rerun"
            disabled={run.status === 'running' || run.status === 'scheduled'}
            onClick={() => rerunRun(run.id, { onSuccess: (data) => navigate(`/projects/${projectId}/runs/${data.run_id}`) })}
            className={toneAction.accent} />
        </div>
      }
    >
      {hasMutationError([rerun, retry]) && (
        <DetailBodyTemplate.Section>
          <MutationErrors of={[rerun, retry]} />
        </DetailBodyTemplate.Section>
      )}
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
      confirmation={confirmation}
      cancelling={cancellingRun}
      onConfirmCancel={() => cancelRun(run.id)}
    />
    </>
  )
}
