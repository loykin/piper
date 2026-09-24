import { PanelTemplate } from '@loykin/designkit'
import StatusBadge from '@/shared/components/StatusBadge'
import type { ServiceHistory } from '@/features/serving/types'
import { fmtDate } from '@/lib/format'
import { PanelCloseButton } from '@/shared/components/PanelPlaceholder'

function elapsed(deployedAt: string, stoppedAt: string): string {
  const ms = new Date(stoppedAt).getTime() - new Date(deployedAt).getTime()
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  if (ms < 3_600_000) return `${(ms / 60000).toFixed(1)}m`
  return `${(ms / 3_600_000).toFixed(1)}h`
}

export function ServingHistoryDetailPanel({ entry }: { entry: ServiceHistory }) {

  return (
    <PanelTemplate
      eyebrow="Service History"
      title={entry.name}
      status={<StatusBadge status={entry.status} />}
      actions={
        <PanelCloseButton />
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
          <PanelTemplate.Row label="Deployed">{fmtDate(entry.deployed_at)}</PanelTemplate.Row>
          <PanelTemplate.Row label="Stopped">{fmtDate(entry.stopped_at)}</PanelTemplate.Row>
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
