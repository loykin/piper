import { useCallback, useMemo, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { DataBodyTemplate } from '@loykin/designkit'
import { DataGrid, type DataGridColumnDef } from '@loykin/gridkit'
import { Plus, Power, RotateCw } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { secretColumns } from '@/features/secrets/columns'
import { usePatchSecret, useRotateSecret, useSecrets } from '@/features/secrets/hooks'
import type { SecretMetadata } from '@/features/secrets/types'
import { useProjectId } from '@/lib/projectContext'
import RotateSecretDialog from './RotateSecretDialog'

export default function SecretsPage() {
  const projectId = useProjectId()
  const navigate = useNavigate()
  const { data = [], isLoading } = useSecrets()
  const patchSecret = usePatchSecret()
  const rotateSecret = useRotateSecret()

  const [rotateTarget, setRotateTarget] = useState<SecretMetadata | null>(null)
  const [actionError, setActionError] = useState('')

  const toggleSecret = useCallback(async (secret: SecretMetadata) => {
    setActionError('')
    const enabled = secret.disabled
    if (!confirm(`${enabled ? 'Enable' : 'Disable'} secret "${secret.name}"?`)) return
    try {
      await patchSecret.mutateAsync({ name: secret.name, patch: { enabled } })
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }, [patchSecret])

  const columns = useMemo<DataGridColumnDef<SecretMetadata>[]>(() => [
    ...secretColumns,
    {
      id: 'actions',
      header: '',
      meta: { minWidth: 120, align: 'right' },
      cell: ({ row }) => (
        <div className="flex justify-end gap-2">
          <IconButton
            icon={<RotateCw />}
            label="Rotate"
            onClick={e => { e.stopPropagation(); setRotateTarget(row.original) }}
            disabled={row.original.disabled}
          />
          <IconButton
            icon={<Power />}
            label={row.original.disabled ? 'Enable' : 'Disable'}
            onClick={e => { e.stopPropagation(); void toggleSecret(row.original) }}
            className={row.original.disabled ? 'text-primary hover:bg-primary/10' : 'text-destructive hover:bg-destructive/10'}
          />
        </div>
      ),
    },
  ], [toggleSecret])

  return (
    <DataBodyTemplate
      title="Secrets"
      description="Project-scoped named key-value credentials. Reference any key from pipeline, notebook, or serving specs."
      actions={
        <Button size="sm" onClick={() => void navigate({ to: `/projects/${projectId}/secrets/new` })}>
          <Plus className="mr-2 size-4" />
          New Secret
        </Button>
      }
    >
      <DataBodyTemplate.Body>
        {actionError && <p className="mb-3 text-sm text-destructive">{actionError}</p>}
        <DataGrid
          data={data}
          columns={columns}
          isLoading={isLoading}
          emptyMessage="No secrets configured."
        />
      </DataBodyTemplate.Body>

      <RotateSecretDialog
        target={rotateTarget}
        rotateSecret={rotateSecret}
        onClose={() => setRotateTarget(null)}
      />
    </DataBodyTemplate>
  )
}
