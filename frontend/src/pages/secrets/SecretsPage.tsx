import { useCallback, useMemo, useState } from 'react'
import { DataBodyTemplate } from '@loykin/designkit'
import { DataGrid, type DataGridColumnDef } from '@loykin/gridkit'
import { KeyRound, Plus, Power, RotateCw } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { secretColumns } from '@/features/secrets/columns'
import { useCreateSecret, usePatchSecret, useRotateSecret, useSecrets } from '@/features/secrets/hooks'
import type { SecretMetadata } from '@/features/secrets/types'

type FormState = {
  name: string
  username: string
  token: string
}

const emptyForm: FormState = { name: '', username: '', token: '' }

export default function SecretsPage() {
  const { data = [], isLoading } = useSecrets()
  const createSecret = useCreateSecret()
  const rotateSecret = useRotateSecret()
  const patchSecret = usePatchSecret()
  const [createOpen, setCreateOpen] = useState(false)
  const [rotateTarget, setRotateTarget] = useState<SecretMetadata | null>(null)
  const [form, setForm] = useState<FormState>(emptyForm)
  const [formError, setFormError] = useState('')
  const [actionError, setActionError] = useState('')

  const toggleSecret = useCallback(async (secret: SecretMetadata) => {
    setActionError('')
    const enabled = secret.disabled
    const verb = enabled ? 'Enable' : 'Disable'
    if (!confirm(`${verb} secret "${secret.name}"?`)) return
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
            onClick={e => {
              e.stopPropagation()
              setFormError('')
              setForm({ ...emptyForm, name: row.original.name })
              setRotateTarget(row.original)
            }}
            disabled={row.original.disabled}
          />
          <IconButton
            icon={<Power />}
            label={row.original.disabled ? 'Enable' : 'Disable'}
            onClick={e => {
              e.stopPropagation()
              void toggleSecret(row.original)
            }}
            className={row.original.disabled ? 'text-primary hover:bg-primary/10' : 'text-destructive hover:bg-destructive/10'}
          />
        </div>
      ),
    },
  ], [toggleSecret])

  async function submitCreate() {
    setFormError('')
    try {
      await createSecret.mutateAsync({
        name: form.name.trim(),
        type: 'git',
        provider: 'piper-managed',
        data: { username: form.username, token: form.token },
      })
      setForm(emptyForm)
      setCreateOpen(false)
    } catch (err) {
      setFormError(err instanceof Error ? err.message : String(err))
    }
  }

  async function submitRotate() {
    if (!rotateTarget) return
    setFormError('')
    try {
      await rotateSecret.mutateAsync({
        name: rotateTarget.name,
        data: { username: form.username, token: form.token },
      })
      setForm(emptyForm)
      setRotateTarget(null)
    } catch (err) {
      setFormError(err instanceof Error ? err.message : String(err))
    }
  }

  const busy = createSecret.isPending || rotateSecret.isPending || patchSecret.isPending

  return (
    <DataBodyTemplate
      title="Secrets"
      description="Manage project-scoped credentials used by pipeline runs."
      actions={
        <Button size="sm" onClick={() => { setFormError(''); setForm(emptyForm); setCreateOpen(true) }}>
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

      <SecretDialog
        title="New Git Secret"
        open={createOpen}
        form={form}
        busy={busy}
        submitLabel="Create"
        onOpenChange={setCreateOpen}
        onFormChange={setForm}
        onSubmit={submitCreate}
        error={formError}
        showName
      />
      <SecretDialog
        title={`Rotate ${rotateTarget?.name ?? ''}`}
        open={!!rotateTarget}
        form={form}
        busy={busy}
        submitLabel="Rotate"
        onOpenChange={open => { if (!open) setRotateTarget(null) }}
        onFormChange={setForm}
        onSubmit={submitRotate}
        error={formError}
      />
    </DataBodyTemplate>
  )
}

function SecretDialog({
  title,
  open,
  form,
  busy,
  submitLabel,
  showName = false,
  onOpenChange,
  onFormChange,
  onSubmit,
  error,
}: {
  title: string
  open: boolean
  form: FormState
  busy: boolean
  submitLabel: string
  showName?: boolean
  onOpenChange: (open: boolean) => void
  onFormChange: (form: FormState) => void
  onSubmit: () => void | Promise<void>
  error?: string
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <KeyRound className="size-4" />
            {title}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          {showName && (
            <div className="space-y-1.5">
              <Label htmlFor="secret-name">Name</Label>
              <Input
                id="secret-name"
                value={form.name}
                onChange={e => onFormChange({ ...form, name: e.target.value })}
                placeholder="github-prod"
              />
            </div>
          )}
          <div className="space-y-1.5">
            <Label htmlFor="secret-username">Username</Label>
            <Input
              id="secret-username"
              value={form.username}
              onChange={e => onFormChange({ ...form, username: e.target.value })}
              placeholder="git-user or bot account"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="secret-token">Token</Label>
            <Input
              id="secret-token"
              type="password"
              value={form.token}
              onChange={e => onFormChange({ ...form, token: e.target.value })}
              placeholder="Personal access token"
            />
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>Cancel</Button>
          <Button onClick={() => void onSubmit()} disabled={busy || (showName && !form.name.trim()) || !form.token}>
            {busy ? 'Saving...' : submitLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
