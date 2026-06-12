import { useEffect, useState } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { useProjectId } from '@/lib/projectContext'
import { RotateCcw, RefreshCw, XCircle, Trash2 } from 'lucide-react'
import { DataPage } from '@loykin/designkit'
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
      <DataPage>
        <DataPage.Content>
          <p className="text-sm text-muted-foreground">Loading…</p>
        </DataPage.Content>
      </DataPage>
    )
  }

  return (
    <DataPage>
      <DataPage.Header>
        <DataPage.TitleBlock
          breadcrumb={<Link to={`/projects/${projectId}/history`} className="hover:text-foreground transition-colors">← History</Link>}
          title={<span className="font-mono text-lg">{run.id}</span>}
        />
        <DataPage.Actions>
          <StatusBadge status={run.status} />
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
        </DataPage.Actions>
      </DataPage.Header>

      <DataPage.Content>
        <RunDAG
          pipelineYaml={run.pipeline_yaml}
          steps={steps}
          selected={selectedStep}
          onSelectStep={setSelectedStep}
        />

        <StepList
          steps={steps}
          selectedId={selectedStep}
          onSelect={setSelectedStep}
          onRetry={(stepName) => {
            retryStep({ runId: run.id, stepId: stepName }, {
              onSuccess: (data) => navigate(`/runs/${data.run_id}`),
              onError: (err) => alert(err.message),
            })
          }}
        />

        <ArtifactPanel runId={id!} artifacts={allArtifacts} />

        <LogViewer runId={id!} stepId={selectedStep} />
      </DataPage.Content>
    </DataPage>
  )
}
