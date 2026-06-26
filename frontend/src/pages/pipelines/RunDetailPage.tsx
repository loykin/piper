import { useEffect, useState } from 'react'
import { useParams, Link, useNavigate } from '@/lib/router'
import { useProjectId } from '@/lib/projectContext'
import { RotateCcw, RefreshCw, XCircle, Trash2 } from 'lucide-react'
import { DetailBodyTemplate } from '@loykin/designkit'
import { IconButton } from '@/components/ui/icon-button'
import { useRun, useRunSteps, useDeleteRun, useCancelRun, useRerunRun, useRetryStep, useStepArtifacts } from '@/features/runs/hooks'
import StatusBadge from '@/shared/components/StatusBadge'
import RunDAG from '@/shared/components/RunDAG'
import { StepList } from '@/features/runs/components/StepList'
import { LogViewer } from '@/features/runs/components/LogViewer'
import { ArtifactPanel } from '@/features/runs/components/ArtifactPanel'

export default function RunDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const projectId = useProjectId()
  const [selectedStep, setSelectedStep] = useState<string | null>(null)

  const { data: run = null, isLoading } = useRun(id!)
  const { data: steps = [] } = useRunSteps(id!)

  const { data: allArtifacts = [] } = useStepArtifacts(id!, selectedStep)

  const { mutate: deleteRun } = useDeleteRun()
  const { mutate: cancelRun } = useCancelRun()
  const { mutate: rerunRun } = useRerunRun()
  const { mutate: retryStep } = useRetryStep()

  useEffect(() => {
    if (steps.length && !selectedStep) {
      setSelectedStep(steps[0].step_name)
    }
  }, [steps, selectedStep])

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
    <DetailBodyTemplate
      eyebrow={<Link to={`/projects/${projectId}/history`} className="hover:text-foreground transition-colors">← History</Link>}
      title={<span className="font-mono">{run.id}</span>}
      status={<StatusBadge status={run.status} />}
      actions={
        <div className="flex items-center gap-0.5">
          <IconButton icon={<XCircle />} label="Cancel Run"
            disabled={run.status !== 'running' && run.status !== 'scheduled'}
            onClick={() => {
              if (!confirm(`Cancel run ${run.id}?`)) return
              cancelRun(run.id)
            }}
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
            onClick={() => {
              if (!confirm(`Delete run ${run.id}?\nArtifacts will also be removed.`)) return
              deleteRun(run.id, { onSuccess: () => navigate(`/projects/${projectId}/history`) })
            }}
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
        <ArtifactPanel runId={id!} artifacts={allArtifacts} />
      </DetailBodyTemplate.Section>

      <DetailBodyTemplate.Section>
        <LogViewer runId={id!} stepId={selectedStep} />
      </DetailBodyTemplate.Section>
    </DetailBodyTemplate>
  )
}
