import { FlaskConical, Power, RotateCw, Trash2, X } from 'lucide-react'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import type { Credential } from '@/features/credentials/types'

function fmtDate(value?: string): string {
  if (!value || value.startsWith('0001-01-01')) return '—'
  const ts = new Date(value)
  if (Number.isNaN(ts.getTime())) return '—'
  return ts.toLocaleString()
}

interface CredentialDetailPanelProps {
  credential: Credential
  onTest: (credential: Credential) => void
  onRotate: (credential: Credential) => void
  onToggle: (credential: Credential) => void
  onDelete: (credential: Credential) => void
}

export function CredentialDetailPanel({ credential, onTest, onRotate, onToggle, onDelete }: CredentialDetailPanelProps) {
  const { close } = useSidePanel()

  const statusBadge = credential.disabled
    ? <Badge variant="secondary">Disabled</Badge>
    : credential.last_test_ok === true
      ? <Badge variant="default">Verified</Badge>
      : credential.last_test_ok === false
        ? <Badge variant="destructive">Failed</Badge>
        : <Badge variant="outline">Active</Badge>

  return (
    <PanelTemplate
      eyebrow={credential.kind}
      title={credential.name}
      status={statusBadge}
      actions={
        <div className="flex items-center gap-1">
          <IconButton
            icon={<FlaskConical />}
            label="Test"
            onClick={() => onTest(credential)}
            disabled={credential.disabled || credential.kind !== 'git'}
          />
          <IconButton
            icon={<RotateCw />}
            label="Rotate"
            onClick={() => onRotate(credential)}
            disabled={credential.disabled}
          />
          <IconButton
            icon={<Power />}
            label={credential.disabled ? 'Enable' : 'Disable'}
            onClick={() => onToggle(credential)}
            className={credential.disabled ? 'text-primary hover:bg-primary/10' : 'text-muted-foreground hover:bg-muted'}
          />
          <IconButton
            icon={<Trash2 />}
            label="Delete"
            onClick={() => { onDelete(credential); void close() }}
            className="text-destructive hover:bg-destructive/10"
          />
          <Button variant="ghost" size="icon-sm" onClick={() => void close()}>
            <X />
            <span className="sr-only">Close</span>
          </Button>
        </div>
      }
    >
      <PanelTemplate.Section title="Details">
        <dl className="space-y-2">
          <PanelTemplate.Row label="Kind"><Badge variant="outline">{credential.kind}</Badge></PanelTemplate.Row>
          <PanelTemplate.Row label={credential.kind === 'generic' ? 'Keys' : 'Endpoint'}>
            <span className="font-mono text-xs text-muted-foreground">
              {credential.kind === 'generic'
                ? (credential.keys?.join(', ') || '—')
                : (credential.endpoint || 'any repo')}
            </span>
          </PanelTemplate.Row>
          <PanelTemplate.Row label="Last Used">{fmtDate(credential.last_used_at)}</PanelTemplate.Row>
          <PanelTemplate.Row label="Last Tested">{fmtDate(credential.last_tested_at)}</PanelTemplate.Row>
          {credential.last_test_message && (
            <PanelTemplate.Row label="Last Test Result">{credential.last_test_message}</PanelTemplate.Row>
          )}
          <PanelTemplate.Row label="Created">{fmtDate(credential.created_at)}</PanelTemplate.Row>
          <PanelTemplate.Row label="Updated">{fmtDate(credential.updated_at)}</PanelTemplate.Row>
        </dl>
      </PanelTemplate.Section>
    </PanelTemplate>
  )
}
