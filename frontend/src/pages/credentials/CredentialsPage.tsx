import { useCallback, useMemo, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { DataBodyTemplate, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@loykin/designkit'
import { DataGrid, type DataGridColumnDef } from '@loykin/gridkit'
import { FlaskConical, Plus, Power, RotateCw, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { IconButton } from '@/components/ui/icon-button'
import { credentialColumns } from '@/features/credentials/columns'
import {
  useCredentials,
  useDeleteCredential,
  usePatchCredential,
  useRotateCredential,
  useTestCredential,
} from '@/features/credentials/hooks'
import type { Credential, CredentialKind } from '@/features/credentials/types'
import { useProjectId } from '@/lib/projectContext'
import RotateCredentialDialog from './RotateCredentialDialog'
import TestCredentialDialog from './TestCredentialDialog'

type KindFilter = 'all' | CredentialKind
type PendingAction =
  | { type: 'toggle'; credential: Credential }
  | { type: 'delete'; credential: Credential }

export default function CredentialsPage() {
  const projectId = useProjectId()
  const navigate = useNavigate()
  const { data = [], isLoading } = useCredentials()
  const patchCredential = usePatchCredential()
  const rotateCredential = useRotateCredential()
  const deleteCredential = useDeleteCredential()
  const testCredential = useTestCredential()

  const [kindFilter, setKindFilter] = useState<KindFilter>('all')
  const [rotateTarget, setRotateTarget] = useState<Credential | null>(null)
  const [testTarget, setTestTarget] = useState<Credential | null>(null)
  const [pendingAction, setPendingAction] = useState<PendingAction | null>(null)
  const [actionError, setActionError] = useState('')

  const filtered = useMemo(
    () => kindFilter === 'all' ? data : data.filter(item => item.kind === kindFilter),
    [data, kindFilter],
  )

  const runPendingAction = useCallback(async () => {
    if (!pendingAction) return
    setActionError('')
    try {
      if (pendingAction.type === 'toggle') {
        await patchCredential.mutateAsync({
          name: pendingAction.credential.name,
          patch: { enabled: pendingAction.credential.disabled },
        })
      } else {
        await deleteCredential.mutateAsync(pendingAction.credential.name)
      }
      setPendingAction(null)
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }, [deleteCredential, patchCredential, pendingAction])

  const columns = useMemo<DataGridColumnDef<Credential>[]>(() => [
    ...credentialColumns,
    {
      id: 'actions',
      header: '',
      meta: { minWidth: 190, align: 'right' },
      cell: ({ row }) => (
        <div className="flex justify-end gap-1">
          <IconButton
            icon={<FlaskConical />}
            label="Test"
            onClick={e => { e.stopPropagation(); setTestTarget(row.original) }}
            disabled={row.original.disabled || row.original.kind !== 'git'}
          />
          <IconButton
            icon={<RotateCw />}
            label="Rotate"
            onClick={e => { e.stopPropagation(); setRotateTarget(row.original) }}
            disabled={row.original.disabled}
          />
          <IconButton
            icon={<Power />}
            label={row.original.disabled ? 'Enable' : 'Disable'}
            onClick={e => { e.stopPropagation(); setPendingAction({ type: 'toggle', credential: row.original }) }}
            className={row.original.disabled ? 'text-primary hover:bg-primary/10' : 'text-muted-foreground hover:bg-muted'}
          />
          <IconButton
            icon={<Trash2 />}
            label="Delete"
            onClick={e => { e.stopPropagation(); setPendingAction({ type: 'delete', credential: row.original }) }}
            className="text-destructive hover:bg-destructive/10"
          />
        </div>
      ),
    },
  ], [])

  const pendingTitle = pendingAction
    ? pendingAction.type === 'delete'
      ? `Delete ${pendingAction.credential.name}`
      : `${pendingAction.credential.disabled ? 'Enable' : 'Disable'} ${pendingAction.credential.name}`
    : ''
  const pendingDescription = pendingAction
    ? pendingAction.type === 'delete'
      ? 'This removes the credential and its encrypted values.'
      : `This ${pendingAction.credential.disabled ? 'enables' : 'disables'} credential use for future resolutions.`
    : ''

  return (
    <DataBodyTemplate
      title="Credentials"
      description="Project-scoped credentials for workload env and Git source access."
      actions={
        <Button size="sm" onClick={() => void navigate({ to: `/projects/${projectId}/credentials/new` })}>
          <Plus className="mr-2 size-4" />
          New Credential
        </Button>
      }
      toolbarLeft={
        <Select value={kindFilter} onValueChange={value => setKindFilter((value ?? 'all') as KindFilter)}>
          <SelectTrigger size="sm" className="w-36">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All kinds</SelectItem>
            <SelectItem value="generic">Generic</SelectItem>
            <SelectItem value="git">Git</SelectItem>
          </SelectContent>
        </Select>
      }
    >
      <DataBodyTemplate.Body>
        {actionError && <p className="mb-3 text-sm text-destructive">{actionError}</p>}
        <DataGrid
          data={filtered}
          columns={columns}
          isLoading={isLoading}
          emptyMessage="No credentials configured."
        />
      </DataBodyTemplate.Body>

      <RotateCredentialDialog
        key={rotateTarget?.name ?? 'rotate-credential'}
        target={rotateTarget}
        rotateCredential={rotateCredential}
        onClose={() => setRotateTarget(null)}
      />
      <TestCredentialDialog
        target={testTarget}
        testCredential={testCredential}
        onClose={() => setTestTarget(null)}
      />
      <Dialog open={!!pendingAction} onOpenChange={open => { if (!open) setPendingAction(null) }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{pendingTitle}</DialogTitle>
            <DialogDescription>{pendingDescription}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPendingAction(null)}>
              Cancel
            </Button>
            <Button
              variant={pendingAction?.type === 'delete' ? 'destructive' : 'default'}
              onClick={() => void runPendingAction()}
              disabled={deleteCredential.isPending || patchCredential.isPending}
            >
              {pendingAction?.type === 'delete' ? 'Delete' : pendingAction?.credential.disabled ? 'Enable' : 'Disable'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </DataBodyTemplate>
  )
}
