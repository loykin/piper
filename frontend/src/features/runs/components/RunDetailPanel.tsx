import { useEffect, useState } from 'react'
import { RotateCcw, RefreshCw, XCircle, Trash2, X } from 'lucide-react'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { useRun, useRunSteps, useDeleteRun, useCancelRun, useRerunRun, useRetryStep, useStepArtifacts } from '@/features/runs/hooks'
import StatusBadge from '@/shared/components/StatusBadge'
import RunDAG from '@/shared/components/RunDAG'
import { StepList } from '@/features/runs/components/StepList'
import { LogViewer } from '@/features/runs/components/LogViewer'
import { ArtifactPanel } from '@/features/runs/components/ArtifactPanel'

export function RunDetailPanel({ id }: { id: string }) {
  const { close, open } = useSidePanel()
  const [selectedStep, setSelectedStep] = useState<string | null>(null)

  const { data: run = null, isLoading } = useRun(id)
  const { data: steps = [] } = useRunSteps(id)
  const { data: allArtifacts = [] } = useStepArtifacts(id, selectedStep)

  const { mutate: deleteRun } = useDeleteRun()
  const { mutate: cancelRun } = useCancelRun()
  const { mutateAsync: rerunRun } = useRerunRun()
  const { mutate: retryStep } = useRetryStep()

  useEffect(() => {
    if (steps.length && !selectedStep) {
      setSelectedStep(steps[0].step_name)
    }
  }, [steps, selectedStep])

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

  return (
    <PanelTemplate
      eyebrow="Run"
      title={run.id}
      status={<StatusBadge status={run.status} />}
      actions={
        <div className="flex items-center gap-1">
          <IconButton icon={<XCircle />} label="Cancel"
            disabled={run.status !== 'running' && run.status !== 'scheduled'}
            onClick={() => {
              if (!confirm(`Cancel run ${run.id}?`)) return
              cancelRun(run.id)
            }}
            className="text-orange-400 hover:bg-orange-950" />
          <IconButton icon={<RotateCcw />} label="Rerun"
            disabled={run.status === 'running' || run.status === 'scheduled'}
            onClick={() => {
              void rerunRun(run.id).then((data) => {
                void close()
                open(<RunDetailPanel id={data.run_id} />, { size: 720 })
              })
            }}
            className="text-indigo-400 hover:bg-indigo-950" />
          <IconButton icon={<RefreshCw />} label="Retry Failed"
            disabled={run.status !== 'failed'}
            onClick={() => {
              void rerunRun(run.id).then((data) => {
                void close()
                open(<RunDetailPanel id={data.run_id} />, { size: 720 })
              })
            }}
            className="text-yellow-400 hover:bg-yellow-950" />
          <IconButton icon={<Trash2 />} label="Delete"
            disabled={run.status === 'running'}
            onClick={() => {
              if (!confirm(`Delete run ${run.id}?\nArtifacts will also be removed.`)) return
              deleteRun(run.id, { onSuccess: () => void close() })
            }}
            className="text-destructive hover:bg-destructive/10" />
          {closeBtn}
        </div>
      }
    >
      <PanelTemplate.Section title="Pipeline">
        <RunDAG
          pipelineYaml={run.pipeline_yaml}
          steps={steps}
          selected={selectedStep}
          onSelectStep={setSelectedStep}
        />
      </PanelTemplate.Section>

      {run.status === 'failed' && steps.some(s => s.status === 'failed' && s.error) && (
        <PanelTemplate.Section>
          <div className="space-y-1.5">
            {steps.filter(s => s.status === 'failed' && s.error).map(s => (
              <div key={s.step_name} className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2">
                <p className="text-xs font-medium text-destructive">{s.step_name}</p>
                <p className="mt-1 whitespace-pre-wrap break-all font-mono text-[11px] text-muted-foreground">{s.error}</p>
              </div>
            ))}
          </div>
        </PanelTemplate.Section>
      )}

      <PanelTemplate.Section title="Steps">
        <StepList
          steps={steps}
          selectedId={selectedStep}
          onSelect={setSelectedStep}
          onRetry={(stepName) => {
            retryStep({ runId: run.id, stepId: stepName }, {
              onSuccess: (data) => {
                void close()
                open(<RunDetailPanel id={data.run_id} />, { size: 720 })
              },
              onError: (err) => alert(err.message),
            })
          }}
        />
      </PanelTemplate.Section>

      <PanelTemplate.Section title="Artifacts">
        <ArtifactPanel runId={id} artifacts={allArtifacts} />
      </PanelTemplate.Section>

      <PanelTemplate.Section title="Logs">
        <LogViewer runId={id} stepId={selectedStep} />
      </PanelTemplate.Section>
    </PanelTemplate>
  )
}
