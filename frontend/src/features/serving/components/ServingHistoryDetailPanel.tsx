import { X } from 'lucide-react'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { Button } from '@/components/ui/button'
import StatusBadge from '@/shared/components/StatusBadge'
import type { ServiceHistory } from '@/features/serving/types'

function elapsed(deployedAt: string, stoppedAt: string): string {
  const ms = new Date(stoppedAt).getTime() - new Date(deployedAt).getTime()
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  if (ms < 3_600_000) return `${(ms / 60000).toFixed(1)}m`
  return `${(ms / 3_600_000).toFixed(1)}h`
}

export function ServingHistoryDetailPanel({ entry }: { entry: ServiceHistory }) {
  const { close } = useSidePanel()

  return (
    <PanelTemplate
      eyebrow="Service history"
      title={entry.name}
      status={<StatusBadge status={entry.status} />}
      actions={
        <Button variant="ghost" size="icon-sm" onClick={() => void close()}>
          <X />
          <span className="sr-only">Close</span>
        </Button>
      }
    >
      <PanelTemplate.Section title="Details">
        <dl className="space-y-2">
          <PanelTemplate.Row label="Artifact">
            <span className="break-all font-mono text-xs text-muted-foreground">{entry.artifact || '—'}</span>
          </PanelTemplate.Row>
          <PanelTemplate.Row label="Source Run">
            <span className="break-all font-mono text-xs text-muted-foreground">{entry.run_id || '—'}</span>
          </PanelTemplate.Row>
          <PanelTemplate.Row label="Namespace">{entry.namespace || 'local'}</PanelTemplate.Row>
          <PanelTemplate.Row label="Endpoint">
            <span className="break-all font-mono text-xs text-muted-foreground">{entry.endpoint || '—'}</span>
          </PanelTemplate.Row>
          <PanelTemplate.Row label="Deployed">{new Date(entry.deployed_at).toLocaleString()}</PanelTemplate.Row>
          <PanelTemplate.Row label="Stopped">{new Date(entry.stopped_at).toLocaleString()}</PanelTemplate.Row>
          <PanelTemplate.Row label="Duration">{elapsed(entry.deployed_at, entry.stopped_at)}</PanelTemplate.Row>
        </dl>
      </PanelTemplate.Section>

      <PanelTemplate.Section title="Service YAML">
        <pre className="overflow-x-auto rounded border border-border bg-muted/30 p-2 text-xs leading-6 text-muted-foreground">
          {entry.yaml || '(empty)'}
        </pre>
      </PanelTemplate.Section>
    </PanelTemplate>
  )
}
