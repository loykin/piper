import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { Power, Trash2, X } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import type { AlertRule } from '../types'

const date = (value?: string) => value ? new Date(value).toLocaleString() : '—'
export function AlertRuleDetailPanel({ rule, onToggle, onDelete }: { rule: AlertRule; onToggle: (rule: AlertRule) => void; onDelete: (rule: AlertRule) => void }) {
  const { close } = useSidePanel()
  return <PanelTemplate eyebrow="Alert rule" title={rule.name} status={<Badge variant={rule.enabled ? 'default' : 'secondary'}>{rule.enabled ? 'Enabled' : 'Disabled'}</Badge>} actions={<div className="flex items-center gap-1"><IconButton icon={<Power />} label={rule.enabled ? 'Disable' : 'Enable'} onClick={() => onToggle(rule)} /><IconButton icon={<Trash2 />} label="Delete" className="text-destructive" onClick={() => { onDelete(rule); void close() }} /><Button variant="ghost" size="icon-sm" onClick={() => void close()}><X /><span className="sr-only">Close</span></Button></div>}>
    <PanelTemplate.Section title="Condition"><dl className="space-y-2"><PanelTemplate.Row label="Source">{rule.on}</PanelTemplate.Row><PanelTemplate.Row label="Expression"><span className="font-mono text-xs">{rule.on === 'event' ? `${rule.event_type}${rule.when ? ` · ${rule.when}` : ''}` : `${rule.metric_key} ${rule.condition}`}</span></PanelTemplate.Row><PanelTemplate.Row label="Cooldown">{rule.cooldown_seconds}s</PanelTemplate.Row><PanelTemplate.Row label="Channels">{rule.notify.join(', ')}</PanelTemplate.Row></dl></PanelTemplate.Section>
    <PanelTemplate.Section title="Delivery"><dl className="space-y-2"><PanelTemplate.Row label="Last matched">{date(rule.last_matched_at)}</PanelTemplate.Row><PanelTemplate.Row label="Last attempted">{date(rule.last_attempted_at)}</PanelTemplate.Row><PanelTemplate.Row label="Last success">{date(rule.last_success_at)}</PanelTemplate.Row>{rule.last_error && <PanelTemplate.Row label="Last error"><span className="text-destructive">{rule.last_error}</span></PanelTemplate.Row>}</dl></PanelTemplate.Section>
  </PanelTemplate>
}
