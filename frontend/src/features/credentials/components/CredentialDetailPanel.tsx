import { FlaskConical, Power, RotateCw, Trash2 } from 'lucide-react'
import { PanelTemplate } from '@loykin/designkit'
import { Badge } from '@/components/ui/badge'
import { IconButton } from '@/components/ui/icon-button'
import { useCredential } from '@/features/credentials/hooks'
import type { Credential } from '@/features/credentials/types'
import { fmtDate } from '@/lib/format'
import { PanelCloseButton, PanelPlaceholder } from '@/shared/components/PanelPlaceholder'

interface CredentialDetailPanelProps {
  name: string
  onTest: (credential: Credential) => void
  onRotate: (credential: Credential) => void
  onToggle: (credential: Credential) => void
  onDelete: (credential: Credential) => void
}

export function CredentialDetailPanel({ name, onTest, onRotate, onToggle, onDelete }: CredentialDetailPanelProps) {
  const query = useCredential(name)
  const credential = query.data
  if (!credential) return <PanelPlaceholder query={query} noun="credential" />

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
            disabled={credential.disabled || !['git', 'slack', 'webhook'].includes(credential.kind)}
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
            onClick={() => onDelete(credential)}
            className="text-destructive hover:bg-destructive/10"
          />
          <PanelCloseButton />
        </div>
      }
    >
      <PanelTemplate.Section title="Details">
        <dl className="space-y-2">
          <PanelTemplate.Row label="Kind"><Badge variant="outline">{credential.kind}</Badge></PanelTemplate.Row>
          <PanelTemplate.Row label={credential.kind === 'generic' ? 'Keys' : credential.kind === 'git' ? 'Endpoint' : 'Configuration'}>
            <span className="font-mono text-xs text-muted-foreground">
              {credential.kind === 'generic'
                ? (credential.keys?.join(', ') || '—')
                : credential.kind === 'git' ? (credential.endpoint || 'any repo') : 'Encrypted and write-only'}
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
