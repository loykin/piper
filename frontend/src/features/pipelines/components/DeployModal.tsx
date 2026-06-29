// pipelines feature — Deploy to schedule modal component
import { CalendarClock } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { parseMaxRuns } from '@/features/schedules/maxRuns'
import { useDeployPipeline } from '../hooks'
import type { PipelineTemplate } from '../types'

interface DeployModalProps {
  template: PipelineTemplate | null
  cron: string
  enabled: boolean
  onCronChange: (cron: string) => void
  onEnabledChange: (enabled: boolean) => void
  onClose: () => void
  onDeployed: (scheduleId: string) => void
  error?: string
}

export function DeployModal({
  template, cron, enabled, onCronChange, onEnabledChange,
  onClose, onDeployed, error,
}: DeployModalProps) {
  const { mutateAsync: deployPipeline, isPending: deploying } = useDeployPipeline()
  const [localError, setLocalError] = useState('')
  const [maxRuns, setMaxRuns] = useState('')

  useEffect(() => {
    setLocalError('')
    setMaxRuns('')
  }, [template?.id])

  async function handleDeploy() {
    if (!template) return
    setLocalError('')
    const parsedMaxRuns = parseMaxRuns(maxRuns)
    if (parsedMaxRuns == null) {
      setLocalError('Max runs must be a non-negative integer.')
      return
    }
    try {
      const schedule = await deployPipeline({ id: template.id, req: { cron, enabled, max_runs: parsedMaxRuns } })
      onClose()
      onDeployed(schedule.id)
    } catch (err) {
      setLocalError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <Dialog open={!!template} onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Deploy to Schedule</DialogTitle>
        </DialogHeader>
        <p className="text-xs text-muted-foreground">
          Creates a new schedule from <strong>{template?.name}</strong>{template ? ` v${template.version}` : ''}.
          The schedule is independent — updating the template later does not affect it.
        </p>
        <div className="mb-4">
          <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Cron Expression</label>
          <Input
            value={cron}
            onChange={e => onCronChange(e.target.value)}
            placeholder="0 2 * * *"
          />
        </div>
        <div className="mb-4 flex items-center gap-2">
          <Switch
            id="deploy-enabled"
            checked={enabled}
            onCheckedChange={onEnabledChange}
          />
          <label htmlFor="deploy-enabled" className="text-xs">Enable immediately</label>
        </div>
        <div className="mb-4">
          <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Max Runs</label>
          <Input
            type="number"
            min={0}
            step={1}
            value={maxRuns}
            onChange={e => setMaxRuns(e.target.value)}
            placeholder="0"
          />
          <p className="mt-1 text-xs text-muted-foreground">0 keeps all completed runs.</p>
        </div>
        {(localError || error) && <p className="mb-3 text-xs text-destructive">{localError || error}</p>}
        <div className="flex justify-end gap-2">
          <Button variant="outline" size="sm" onClick={onClose} disabled={deploying}>Cancel</Button>
          <Button size="sm" onClick={() => void handleDeploy()} disabled={deploying || !cron.trim()}>
            <CalendarClock size={14} className="mr-1.5" />
            {deploying ? 'Deploying…' : 'Deploy'}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
