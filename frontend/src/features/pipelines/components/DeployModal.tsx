// pipelines feature — Deploy to schedule modal component
import { CalendarClock } from 'lucide-react'
import { useState } from 'react'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useDeployPipeline } from '../hooks'
import type { PipelineTemplate } from '../types'

interface DeployModalProps {
  template: PipelineTemplate | null
  cron: string
  enabled: boolean
  onCronChange: (cron: string) => void
  onEnabledChange: (enabled: boolean) => void
  onClose: () => void
  onDeployed: () => void
  error?: string
}

export function DeployModal({
  template, cron, enabled, onCronChange, onEnabledChange,
  onClose, onDeployed, error,
}: DeployModalProps) {
  const { mutateAsync: deployPipeline, isPending: deploying } = useDeployPipeline()
  const [localError, setLocalError] = useState('')

  async function handleDeploy() {
    if (!template) return
    setLocalError('')
    try {
      await deployPipeline({ id: template.id, req: { cron, enabled } })
      onDeployed()
      onClose()
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
          Creates a new schedule with a snapshot of <strong>{template?.name}</strong>.
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
          <input
            id="deploy-enabled"
            type="checkbox"
            checked={enabled}
            onChange={e => onEnabledChange(e.target.checked)}
            className="rounded"
          />
          <label htmlFor="deploy-enabled" className="text-xs">Enable immediately</label>
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
