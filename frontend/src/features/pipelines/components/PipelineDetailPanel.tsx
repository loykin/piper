import { CalendarClock, CopyPlus, Play, Trash2, X } from 'lucide-react'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Badge } from '@/components/ui/badge'
import { YamlMirror } from '@/components/ui/yaml-mirror'
import type { PipelineTemplate } from '../types'

interface Props {
  template: PipelineTemplate
  onRun: (t: PipelineTemplate) => void
  onDeploy: (t: PipelineTemplate) => void
  onNewVersion: (t: PipelineTemplate) => void
  onDelete: (t: PipelineTemplate) => void
}

export function PipelineDetailPanel({ template: t, onRun, onDeploy, onNewVersion, onDelete }: Props) {
  const { close } = useSidePanel()

  const closeBtn = (
    <Button variant="ghost" size="icon-sm" onClick={() => void close()}>
      <X className="h-3.5 w-3.5" />
    </Button>
  )

  return (
    <PanelTemplate
      eyebrow={`v${t.version}`}
      title={t.name}
      actions={
        <div className="flex items-center gap-1">
          <IconButton icon={<Play />} label="Run" onClick={() => { onRun(t); void close() }} />
          <IconButton icon={<CalendarClock />} label="Deploy to schedule" onClick={() => { onDeploy(t); void close() }} />
          <IconButton icon={<CopyPlus />} label={`New version from v${t.version}`} onClick={() => { onNewVersion(t); void close() }} />
          <IconButton
            icon={<Trash2 />} label="Delete"
            onClick={() => { onDelete(t); void close() }}
            className="text-destructive hover:bg-destructive/10"
          />
          {closeBtn}
        </div>
      }
    >
      <PanelTemplate.Section title="Details">
        <dl className="space-y-2">
          <PanelTemplate.Row label="Version">v{t.version}</PanelTemplate.Row>
          <PanelTemplate.Row label="Submitted">{new Date(t.created_at).toLocaleString()}</PanelTemplate.Row>
          {t.volume_id && (
            <PanelTemplate.Row label="Volume">{t.volume_id}</PanelTemplate.Row>
          )}
          <PanelTemplate.Row label="Version ID">{t.id}</PanelTemplate.Row>
          <PanelTemplate.Row label="Snapshot ID">{t.snapshot_id}</PanelTemplate.Row>
        </dl>
      </PanelTemplate.Section>

      {t.description && (
        <PanelTemplate.Section title="Description">
          <p className="text-sm text-muted-foreground">{t.description}</p>
        </PanelTemplate.Section>
      )}

      {t.tags && t.tags.length > 0 && (
        <PanelTemplate.Section title="Tags">
          <div className="flex flex-wrap gap-1.5">
            {t.tags.map(tag => (
              <Badge key={tag} variant="secondary">{tag}</Badge>
            ))}
          </div>
        </PanelTemplate.Section>
      )}

      <PanelTemplate.Section title="Pipeline YAML">
        <YamlMirror value={t.yaml} readOnly />
      </PanelTemplate.Section>
    </PanelTemplate>
  )
}
