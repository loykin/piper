import { useState } from 'react'
import { ChevronRight, Plus, Trash2 } from 'lucide-react'
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm } from 'react-hook-form'
import { z } from 'zod'
import { DataBodyTemplate, FormField } from '@loykin/designkit'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { useCreateSystemCredential, useDeleteSystemCredential } from '@/features/credentials/hooks'
import type { Credential, CredentialKind } from '@/features/credentials/types'
import { errorMessage } from '@/lib/format'
import { BACKEND_LABELS, type StorageBackendType } from '@/features/storage/backendUrl'
import { useDeleteTarget } from '@/shared/hooks/useDeleteTarget'
import { InstanceScopedBadge } from './ArtifactStoreConfigSection'
import { ConfirmDialog } from '@/shared/components/ConfirmDialog'

// ── System Credentials ──────────────────────────────────────────────────────
// The only editable part of this page now that Artifact Store Config above
// is read-only. Named, live-editable credential entries referenced by name
// from the diagnostic view above (via storage.credentialRef) are the same
// safe pattern Airflow's own Connections feature uses — deleting or
// rotating a credential's keys never risks the artifact-unreachable problem
// a live backend swap does. The create form is a secondary, collapsed-by-
// default action so the section itself doesn't read as another editable
// "S3 settings" card sitting next to the read-only one above it.

// New-credential sub-form fields, superset across kinds — only the fields
// relevant to the active backend's credential kind are ever rendered or
// required.
const EMPTY_DRAFT = { name: '', accessKeyId: '', secretAccessKey: '', serviceAccountJSON: '', accountName: '', accountKey: '' }

function credentialDraftSchema(kind: CredentialKind) {
  const required: Record<string, [keyof typeof EMPTY_DRAFT, string][]> = {
    s3: [['accessKeyId', 'access_key_id'], ['secretAccessKey', 'secret_access_key']],
    gcs: [['serviceAccountJSON', 'service_account_json']],
    azure: [['accountName', 'account_name'], ['accountKey', 'account_key']],
  }
  return z.object({
    name: z.string().trim().min(1, 'Name is required.'),
    accessKeyId: z.string(),
    secretAccessKey: z.string(),
    serviceAccountJSON: z.string(),
    accountName: z.string(),
    accountKey: z.string(),
  }).superRefine((values, ctx) => {
    for (const [field, label] of required[kind] ?? []) {
      if (!values[field].trim()) ctx.addIssue({ code: 'custom', path: [field], message: `${label} is required.` })
    }
  })
}

type CredentialDraft = typeof EMPTY_DRAFT

interface StorageCredentialsSectionProps {
  backend: StorageBackendType
  activeCredentialKind: CredentialKind
  backendCredentials: Credential[]
  credentialRef: string
}

export function StorageCredentialsSection({
  backend, activeCredentialKind, backendCredentials, credentialRef,
}: StorageCredentialsSectionProps) {
  const createSystemCredential = useCreateSystemCredential()
  const deleteSystemCredential = useDeleteSystemCredential()
  const [credentialError, setCredentialError] = useState('')
  const [addOpen, setAddOpen] = useState(false)
  const deleteTarget = useDeleteTarget<string>()
  const { register, handleSubmit, reset, formState: { errors } } = useForm<CredentialDraft>({
    resolver: zodResolver(credentialDraftSchema(activeCredentialKind)),
    defaultValues: EMPTY_DRAFT,
  })

  async function createCredential(draft: CredentialDraft) {
    setCredentialError('')
    try {
      const data: Record<string, string> =
        activeCredentialKind === 's3'
          ? { access_key_id: draft.accessKeyId.trim(), secret_access_key: draft.secretAccessKey.trim() }
          : activeCredentialKind === 'gcs'
            ? { service_account_json: draft.serviceAccountJSON.trim() }
            : { account_name: draft.accountName.trim(), account_key: draft.accountKey.trim() }
      await createSystemCredential.mutateAsync({ name: draft.name.trim(), kind: activeCredentialKind, data })
      reset(EMPTY_DRAFT)
      setAddOpen(false)
    } catch (err) {
      setCredentialError(errorMessage(err))
    }
  }

  return (
    <>
      <DataBodyTemplate.Group
        layout="stacked"
        title={<>System {BACKEND_LABELS[backend]} Credentials<InstanceScopedBadge /></>}
        description="Access keys for the artifact store, referenced by name from storage.credentialRef above. Values are write-only."
      >
        {backendCredentials.length > 0 && (
          <div className="space-y-1">
            {backendCredentials.map(c => (
              <div key={c.name} className="flex items-center justify-between rounded-md border border-border px-3 py-2">
                <span className="font-mono text-sm">{c.name}</span>
                <div className="flex items-center gap-2">
                  {credentialRef === c.name && <Badge variant="secondary">in use</Badge>}
                  {c.disabled && <Badge variant="secondary">disabled</Badge>}
                  <IconButton
                    icon={<Trash2 />}
                    label="Delete"
                    onClick={() => deleteTarget.requestDelete(c.name)}
                    className="text-muted-foreground hover:text-destructive"
                  />
                </div>
              </div>
            ))}
          </div>
        )}

        <Collapsible open={addOpen} onOpenChange={setAddOpen} className="group/add-credential">
          <div className="flex justify-end pt-2">
            <CollapsibleTrigger
              render={<Button type="button" variant="outline" size="sm" />}
            >
              <Plus className="mr-1.5 size-3.5" />
              New credential
              <ChevronRight className="ml-1.5 size-3.5 transition-transform duration-200 group-data-open/add-credential:rotate-90" />
            </CollapsibleTrigger>
          </div>
          <CollapsibleContent>
            <form className="max-w-xl space-y-3 pt-3" noValidate onSubmit={handleSubmit(createCredential)}>
              <FormField label="Name" htmlFor="storage-credential-name" error={errors.name?.message}>
                <Input
                  id="storage-credential-name"
                  placeholder={`${backend}-artifacts`}
                  className="font-mono"
                  aria-invalid={!!errors.name}
                  {...register('name')}
                />
              </FormField>

              {activeCredentialKind === 's3' && (
                <>
                  <FormField label="access_key_id" htmlFor="storage-credential-accessKeyId" error={errors.accessKeyId?.message}>
                    <Input
                      id="storage-credential-accessKeyId"
                      aria-invalid={!!errors.accessKeyId}
                      className="font-mono text-sm"
                      {...register('accessKeyId')}
                    />
                  </FormField>
                  <FormField label="secret_access_key" htmlFor="storage-credential-secretAccessKey" error={errors.secretAccessKey?.message}>
                    <Input
                      id="storage-credential-secretAccessKey"
                      type="password"
                      aria-invalid={!!errors.secretAccessKey}
                      className="font-mono text-sm"
                      {...register('secretAccessKey')}
                    />
                  </FormField>
                </>
              )}

              {activeCredentialKind === 'gcs' && (
                <FormField
                  label="service_account_json"
                  htmlFor="storage-credential-serviceAccountJSON"
                  error={errors.serviceAccountJSON?.message}
                  helperText="Paste the full service-account JSON key file content."
                >
                  <textarea
                    id="storage-credential-serviceAccountJSON"
                    rows={6}
                    placeholder='{"type": "service_account", ...}'
                    aria-invalid={!!errors.serviceAccountJSON}
                    className="w-full rounded-md border border-input bg-background p-2 font-mono text-xs"
                    {...register('serviceAccountJSON')}
                  />
                </FormField>
              )}

              {activeCredentialKind === 'azure' && (
                <>
                  <FormField label="account_name" htmlFor="storage-credential-accountName" error={errors.accountName?.message}>
                    <Input
                      id="storage-credential-accountName"
                      aria-invalid={!!errors.accountName}
                      className="font-mono text-sm"
                      {...register('accountName')}
                    />
                  </FormField>
                  <FormField label="account_key" htmlFor="storage-credential-accountKey" error={errors.accountKey?.message}>
                    <Input
                      id="storage-credential-accountKey"
                      type="password"
                      aria-invalid={!!errors.accountKey}
                      className="font-mono text-sm"
                      {...register('accountKey')}
                    />
                  </FormField>
                </>
              )}

              {credentialError && <p className="text-sm text-destructive">{credentialError}</p>}
              <div className="flex justify-end pt-2">
                <Button type="submit" size="sm" disabled={createSystemCredential.isPending}>
                  {createSystemCredential.isPending ? 'Creating…' : `Add ${activeCredentialKind} Credential`}
                </Button>
              </div>
            </form>
          </CollapsibleContent>
        </Collapsible>
      </DataBodyTemplate.Group>

      <ConfirmDialog
        open={deleteTarget.open}
        onCancel={deleteTarget.cancel}
        title="Delete this system credential?"
        description={<>&quot;{deleteTarget.target}&quot; will be permanently deleted.
{credentialRef === deleteTarget.target && ' It is currently referenced by storage.credentialRef — deleting it will make the artifact store unavailable after the next restart until storage.yaml is updated.'}</>}
        error={deleteTarget.error}
        confirmLabel={deleteSystemCredential.isPending ? 'Deleting…' : 'Delete credential'}
        pending={deleteSystemCredential.isPending}
        onConfirm={() => void deleteTarget.confirm(name => deleteSystemCredential.mutateAsync(name))}
      />
    </>
  )
}
