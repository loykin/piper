import { X } from 'lucide-react'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { Button } from '@/components/ui/button'
import StatusBadge from '@/shared/components/StatusBadge'
import type { NotebookHistory } from '@/features/notebooks/types'

function elapsed(deployedAt: string, stoppedAt: string): string {
  const ms = new Date(stoppedAt).getTime() - new Date(deployedAt).getTime()
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  if (ms < 3_600_000) return `${(ms / 60000).toFixed(1)}m`
  return `${(ms / 3_600_000).toFixed(1)}h`
}

export function NotebookHistoryDetailPanel({ entry }: { entry: NotebookHistory }) {
  const { close } = useSidePanel()

  return (
    <PanelTemplate
      eyebrow="Notebook history"
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
          <PanelTemplate.Row label="Started">{new Date(entry.deployed_at).toLocaleString()}</PanelTemplate.Row>
          <PanelTemplate.Row label="Ended">{new Date(entry.stopped_at).toLocaleString()}</PanelTemplate.Row>
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
