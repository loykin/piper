import { PanelTemplate } from '@loykin/designkit'
import StatusBadge from '@/shared/components/StatusBadge'
import type { NotebookHistory } from '@/features/notebooks/types'
import { fmtDate } from '@/lib/format'
import { PanelCloseButton } from '@/shared/components/PanelPlaceholder'

function elapsed(deployedAt: string, stoppedAt: string): string {
  const ms = new Date(stoppedAt).getTime() - new Date(deployedAt).getTime()
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  if (ms < 3_600_000) return `${(ms / 60000).toFixed(1)}m`
  return `${(ms / 3_600_000).toFixed(1)}h`
}

export function NotebookHistoryDetailPanel({ entry }: { entry: NotebookHistory }) {

  return (
    <PanelTemplate
      eyebrow="Notebook History"
      title={entry.name}
      status={<StatusBadge status={entry.status} />}
      actions={
        <PanelCloseButton />
      }
    >
      <PanelTemplate.Section title="Details">
        <dl className="space-y-2">
          <PanelTemplate.Row label="Image">
            <span className="break-all font-mono text-xs text-muted-foreground">{entry.image || '—'}</span>
          </PanelTemplate.Row>
          <PanelTemplate.Row label="Runtime">
            <span className="break-all font-mono text-xs text-muted-foreground">{entry.runtime_id || '—'}</span>
          </PanelTemplate.Row>
          <PanelTemplate.Row label="Volume">
            <span className="break-all font-mono text-xs text-muted-foreground">{entry.volume_id || '—'}</span>
          </PanelTemplate.Row>
          <PanelTemplate.Row label="Work Dir">
            <span className="break-all font-mono text-xs text-muted-foreground">{entry.work_dir || '—'}</span>
          </PanelTemplate.Row>
          <PanelTemplate.Row label="Endpoint">
            <span className="break-all font-mono text-xs text-muted-foreground">{entry.endpoint || '—'}</span>
          </PanelTemplate.Row>
          <PanelTemplate.Row label="Started">{fmtDate(entry.deployed_at)}</PanelTemplate.Row>
          <PanelTemplate.Row label="Ended">{fmtDate(entry.stopped_at)}</PanelTemplate.Row>
          <PanelTemplate.Row label="Duration">{elapsed(entry.deployed_at, entry.stopped_at)}</PanelTemplate.Row>
        </dl>
      </PanelTemplate.Section>

      <PanelTemplate.Section title="Notebook YAML">
        <pre className="overflow-x-auto rounded border border-border bg-muted/30 p-2 text-xs leading-6 text-muted-foreground">
          {entry.yaml || '(empty)'}
        </pre>
      </PanelTemplate.Section>
    </PanelTemplate>
  )
}
