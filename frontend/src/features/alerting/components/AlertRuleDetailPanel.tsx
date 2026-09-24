import { PanelTemplate } from '@loykin/designkit'
import { Power, Trash2 } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { IconButton } from '@/components/ui/icon-button'
import { fmtDate } from '@/lib/format'
import { PanelCloseButton, PanelPlaceholder } from '@/shared/components/PanelPlaceholder'
import { useAlertRule } from '../hooks'
import type { AlertRule } from '../types'

export function AlertRuleDetailPanel({ id, onToggle, onDelete }: {
  id: string
  onToggle: (rule: AlertRule) => void
  onDelete: (rule: AlertRule) => void
}) {
  const query = useAlertRule(id)
  const rule = query.data
  if (!rule) return <PanelPlaceholder query={query} noun="alert rule" />

  return (
    <PanelTemplate
      eyebrow="Alert Rule"
      title={rule.name}
      status={<Badge variant={rule.enabled ? 'default' : 'secondary'}>{rule.enabled ? 'Enabled' : 'Disabled'}</Badge>}
      actions={
        <div className="flex items-center gap-1">
          <IconButton icon={<Power />} label={rule.enabled ? 'Disable' : 'Enable'} onClick={() => onToggle(rule)} />
          <IconButton icon={<Trash2 />} label="Delete" className="text-destructive hover:bg-destructive/10" onClick={() => onDelete(rule)} />
          <PanelCloseButton />
        </div>
      }
    >
      <PanelTemplate.Section title="Condition">
        <dl className="space-y-2">
          <PanelTemplate.Row label="Source">{rule.on}</PanelTemplate.Row>
          <PanelTemplate.Row label="Expression">
            <span className="font-mono text-xs">
              {rule.on === 'event'
                ? `${rule.event_type}${rule.when ? ` · ${rule.when}` : ''}`
                : `${rule.metric_key} ${rule.condition}`}
            </span>
          </PanelTemplate.Row>
          <PanelTemplate.Row label="Cooldown">{rule.cooldown_seconds}s</PanelTemplate.Row>
          <PanelTemplate.Row label="Channels">{rule.notify.join(', ')}</PanelTemplate.Row>
        </dl>
      </PanelTemplate.Section>
      <PanelTemplate.Section title="Delivery">
        <dl className="space-y-2">
          <PanelTemplate.Row label="Last Matched">{fmtDate(rule.last_matched_at)}</PanelTemplate.Row>
          <PanelTemplate.Row label="Last Attempted">{fmtDate(rule.last_attempted_at)}</PanelTemplate.Row>
          <PanelTemplate.Row label="Last Success">{fmtDate(rule.last_success_at)}</PanelTemplate.Row>
          {rule.last_error && (
            <PanelTemplate.Row label="Last Error"><span className="text-destructive">{rule.last_error}</span></PanelTemplate.Row>
          )}
        </dl>
      </PanelTemplate.Section>
    </PanelTemplate>
  )
}
