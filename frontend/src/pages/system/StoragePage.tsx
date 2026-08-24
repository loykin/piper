import { Fragment, useEffect, useRef, useState } from 'react'
import { useProjectId } from '@/lib/projectContext'
import { useSearchParams } from '@/lib/router'
import { CheckCircle2, Download, Folder, FolderOpen, Plus, RefreshCw, Save, Search, Trash2, XCircle } from 'lucide-react'
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
  DataBodyTemplate,
  FormField,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@loykin/designkit'
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { FilterInput } from '@loykin/filter-input'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import {
  useSystemCredentials,
  useCreateSystemCredential,
  useDeleteSystemCredential,
} from '@/features/credentials/hooks'
import type { Credential, CredentialKind } from '@/features/credentials/types'
import {
  useStorageSettings,
  useStorageObjectsPaged,
  useSaveStorageSettings,
  useTestStorageSettings,
  useDeleteObject,
  useUploadObject,
} from '@/features/storage/hooks'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { RowActions } from '@/shared/components/RowActions'
import { storageObjectURL, type StorageObjectInfo, type StorageSettingsView } from '@/features/storage/api'
import { fmtBytes, fmtDate } from '@/features/storage/format'
import { ObjectDetailPanel } from '@/features/storage/components/ObjectDetailPanel'
import {
  BACKEND_CREDENTIAL_KIND,
  BACKEND_LABELS,
  composeStorageURL,
  emptyBackendForm,
  parseStorageURL,
  type BackendForm,
  type StorageBackendType,
} from '@/features/storage/backendUrl'

// This settings surface (backend + credentials) always edits the Piper
// instance actually serving this UI — never a remote federation Member. A
// project owned by a Member has its own separate storage.url, configured on
// that Member's own instance instead. Upload Object / Uploaded Objects below
// are the opposite: they're project-scoped and correctly relay to whichever
// Member owns the current project, so this badge stays on the two groups
// that are actually instance-scoped rather than the whole page.
function InstanceScopedBadge() {
  return (
    <Badge
      variant="outline"
      className="ml-2 align-middle text-xs font-normal"
      title="Configures only this Piper instance's own storage — not any project owned by a remote federation Member. Check the project switcher's (member) label."
    >
      This instance only
    </Badge>
  )
}

function statusVariant(status: StorageSettingsView['effective']['status']): 'default' | 'secondary' | 'destructive' {
  switch (status) {
    case 'enabled':     return 'default'
    case 'unavailable': return 'destructive'
    default:            return 'secondary'
  }
}

type FormUpdater = (updater: (prev: BackendForm) => BackendForm) => void

// ── per-backend field clusters ──────────────────────────────────────────────
// Each backend owns exactly the fields it reads out of BackendForm — see
// composeStorageURL in features/storage/backendUrl.ts for how these map onto
// the storage.url scheme (s3://, gs://, azblob://, https://).

function FileFields() {
  return (
    <DataBodyTemplate.Field
      label=""
      description="Stores artifacts under this server's own output directory. No further configuration needed."
    >
      <span className="text-xs text-muted-foreground">file://{'{output_dir}'}/store</span>
    </DataBodyTemplate.Field>
  )
}

function S3Fields({ form, onChange }: { form: BackendForm; onChange: FormUpdater }) {
  return (
    <>
      <DataBodyTemplate.Row label="Bucket">
        <Input
          value={form.bucket}
          onChange={e => onChange(prev => ({ ...prev, bucket: e.target.value }))}
          placeholder="piper-artifacts"
          className="font-mono text-sm"
        />
      </DataBodyTemplate.Row>
      <DataBodyTemplate.Row label="Endpoint" description="Leave empty for AWS S3. Set for MinIO, SeaweedFS, R2, etc.">
        <Input
          value={form.endpoint}
          onChange={e => onChange(prev => ({ ...prev, endpoint: e.target.value }))}
          placeholder="http://localhost:9000"
          className="font-mono text-sm"
        />
      </DataBodyTemplate.Row>
      <DataBodyTemplate.Row label="Region">
        <Input
          value={form.region}
          onChange={e => onChange(prev => ({ ...prev, region: e.target.value }))}
          placeholder="us-east-1"
          className="font-mono text-sm"
        />
      </DataBodyTemplate.Row>
      <DataBodyTemplate.Row label="Force path style" description="Required by most non-AWS S3-compatible servers.">
        <Switch
          checked={form.forcePathStyle}
          onCheckedChange={checked => onChange(prev => ({ ...prev, forcePathStyle: checked }))}
        />
      </DataBodyTemplate.Row>
    </>
  )
}

function GcsFields({ form, onChange }: { form: BackendForm; onChange: FormUpdater }) {
  return (
    <DataBodyTemplate.Row label="Bucket">
      <Input
        value={form.bucket}
        onChange={e => onChange(prev => ({ ...prev, bucket: e.target.value }))}
        placeholder="piper-artifacts"
        className="font-mono text-sm"
      />
    </DataBodyTemplate.Row>
  )
}

function AzureFields({ form, onChange }: { form: BackendForm; onChange: FormUpdater }) {
  return (
    <DataBodyTemplate.Row label="Container">
      <Input
        value={form.bucket}
        onChange={e => onChange(prev => ({ ...prev, bucket: e.target.value }))}
        placeholder="piper-artifacts"
        className="font-mono text-sm"
      />
    </DataBodyTemplate.Row>
  )
}

function HttpFields({
  form, onChange, token, onTokenChange,
}: { form: BackendForm; onChange: FormUpdater; token: string; onTokenChange: (v: string) => void }) {
  return (
    <>
      <DataBodyTemplate.Row label="Base URL">
        <Input
          value={form.httpURL}
          onChange={e => onChange(prev => ({ ...prev, httpURL: e.target.value }))}
          placeholder="https://store.example.internal"
          className="font-mono text-sm"
        />
      </DataBodyTemplate.Row>
      <DataBodyTemplate.Row label="Bearer token" description="Sent as Authorization: Bearer <token> to the base URL above.">
        <Input value={token} onChange={e => onTokenChange(e.target.value)} placeholder="Bearer token for HTTP stores" />
      </DataBodyTemplate.Row>
    </>
  )
}

// ── Artifact Store Config ───────────────────────────────────────────────────
// Owns the save boundary for "which backend, and how do we reach it": backend
// selection, that backend's own fields, which system credential supplies its
// keys, and a Test Connection check before committing to Save.

interface ArtifactStoreConfigSectionProps {
  storage: StorageSettingsView | null
  isLoading: boolean
  loadError: unknown
  disabled: boolean
  setDisabled: (v: boolean) => void
  token: string
  setToken: (v: string) => void
  credentialRef: string
  setCredentialRef: (v: string) => void
  backendForm: BackendForm
  setBackendForm: React.Dispatch<React.SetStateAction<BackendForm>>
  activeCredentialKind: CredentialKind | undefined
  backendCredentials: Credential[]
}

function ArtifactStoreConfigSection({
  storage, isLoading, loadError, disabled, setDisabled, token, setToken, credentialRef, setCredentialRef,
  backendForm, setBackendForm, activeCredentialKind, backendCredentials,
}: ArtifactStoreConfigSectionProps) {
  const saveSettings = useSaveStorageSettings()
  const testSettings = useTestStorageSettings()

  if (isLoading) {
    return (
      <DataBodyTemplate.Group layout="stacked" title={<>Artifact Store Config<InstanceScopedBadge /></>}>
        <p className="text-sm text-muted-foreground">Loading…</p>
      </DataBodyTemplate.Group>
    )
  }

  if (loadError) {
    return (
      <DataBodyTemplate.Group layout="stacked" title={<>Artifact Store Config<InstanceScopedBadge /></>}>
        <p className="text-sm text-destructive">
          Couldn&apos;t load storage configuration:{' '}
          {loadError instanceof Error ? loadError.message : String(loadError)}.
          {' '}This page only shows config for this Piper instance — a system admin on this
          instance can check permissions or try again.
        </p>
      </DataBodyTemplate.Group>
    )
  }

  const status = storage?.effective.status ?? 'disabled'
  const backend = storage?.effective.backend || '—'

  // Bucket/URL is required per non-file backend — otherwise composeStorageURL
  // returns '' and the test/save would silently fall back to file storage,
  // reporting a misleading "connected" for a backend that was never tested.
  const canTest = (() => {
    switch (backendForm.backend) {
      case 's3':
      case 'gcs':
      case 'azure':
        return !!backendForm.bucket.trim()
      case 'http':
        return !!backendForm.httpURL.trim()
      default:
        return true
    }
  })()

  // Switching backend invalidates the previous backend's credentialRef —
  // an s3 credential name is never a valid azure credentialRef.
  function handleBackendChange(next: StorageBackendType) {
    setBackendForm(prev => ({ ...emptyBackendForm(), backend: next, httpURL: next === 'http' ? prev.httpURL : '' }))
    setCredentialRef('')
    testSettings.reset()
  }

  function currentConfig() {
    return { disabled, url: composeStorageURL(backendForm), token, credentialRef: credentialRef || undefined }
  }

  function handleSave() {
    saveSettings.mutate(currentConfig(), {
      onSuccess: (next) => {
        setDisabled(next.config.disabled)
        setToken(next.config.token)
        setCredentialRef(next.config.credentialRef ?? '')
        setBackendForm(parseStorageURL(next.config.url))
      },
    })
  }

  function handleTest() {
    if (!canTest) return
    testSettings.mutate(currentConfig())
  }

  return (
    <DataBodyTemplate.Group
      layout="stacked"
      title={<>Artifact Store Config<InstanceScopedBadge /></>}
    >
      <DataBodyTemplate.Row
        label="Enabled"
        description="Disabled hides object store exports and downloads after restart."
      >
        <Switch checked={!disabled} onCheckedChange={(checked) => setDisabled(!checked)} />
      </DataBodyTemplate.Row>

      <DataBodyTemplate.Row
        label="Backend"
        description="Selects the artifact store implementation. Only the fields this backend actually uses are shown below."
      >
        <Select value={backendForm.backend} onValueChange={v => v && handleBackendChange(v as StorageBackendType)}>
          <SelectTrigger className="w-64"><SelectValue /></SelectTrigger>
          <SelectContent>
            {(Object.entries(BACKEND_LABELS) as [StorageBackendType, string][]).map(([value, label]) => (
              <SelectItem key={value} value={value}>{label}</SelectItem>
            ))}
          </SelectContent>
        </Select>
      </DataBodyTemplate.Row>

      {backendForm.backend === 'file' && <FileFields />}
      {backendForm.backend === 's3' && <S3Fields form={backendForm} onChange={setBackendForm} />}
      {backendForm.backend === 'gcs' && <GcsFields form={backendForm} onChange={setBackendForm} />}
      {backendForm.backend === 'azure' && <AzureFields form={backendForm} onChange={setBackendForm} />}
      {backendForm.backend === 'http' && (
        <HttpFields form={backendForm} onChange={setBackendForm} token={token} onTokenChange={setToken} />
      )}

      {activeCredentialKind && (
        <DataBodyTemplate.Row
          label="Credential"
          description={`System ${activeCredentialKind} credential supplying access keys. Manage credentials below.`}
        >
          <Select
            value={credentialRef || '__none__'}
            onValueChange={v => setCredentialRef(v === '__none__' ? '' : (v ?? ''))}
          >
            <SelectTrigger className="w-72"><SelectValue placeholder="None" /></SelectTrigger>
            <SelectContent>
              <SelectItem value="__none__">None</SelectItem>
              {backendCredentials.map(c => (
                <SelectItem key={c.name} value={c.name}>{c.name}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </DataBodyTemplate.Row>
      )}

      <DataBodyTemplate.Field
        label="Config file"
        description="Saved here. Apply requires a server restart."
      >
        <span className="break-all font-mono text-xs">{storage?.config_path || '—'}</span>
      </DataBodyTemplate.Field>

      <DataBodyTemplate.Field label="Runtime status">
        <div className="space-y-1 text-sm">
          <p><span className="text-muted-foreground">Status: </span>{status}</p>
          <p><span className="text-muted-foreground">Backend: </span>{backend}</p>
          <p><span className="text-muted-foreground">Reason: </span>{storage?.effective.reason || '—'}</p>
        </div>
      </DataBodyTemplate.Field>

      <div className="flex items-center justify-end gap-2 pt-2">
        {!canTest && (
          <span className="text-xs text-muted-foreground">
            {backendForm.backend === 'http' ? 'Enter a base URL first.' : 'Enter a bucket/container first.'}
          </span>
        )}
        {canTest && testSettings.data && (
          <span className={`flex items-center gap-1.5 text-xs ${testSettings.data.ok ? 'text-primary' : 'text-destructive'}`}>
            {testSettings.data.ok ? <CheckCircle2 className="size-3.5" /> : <XCircle className="size-3.5" />}
            {testSettings.data.message}
          </span>
        )}
        <Button type="button" variant="outline" size="sm" onClick={handleTest} disabled={testSettings.isPending || !canTest}>
          {testSettings.isPending ? 'Testing…' : 'Test Connection'}
        </Button>
        <Button
          size="sm"
          onClick={handleSave}
          disabled={saveSettings.isPending || !storage || (!disabled && !canTest)}
          title={!disabled && !canTest ? 'Enter the required backend field before saving.' : undefined}
        >
          <Save className="mr-2 size-4" />
          {saveSettings.isPending ? 'Saving…' : 'Save'}
        </Button>
      </div>
    </DataBodyTemplate.Group>
  )
}

// ── System Credentials ──────────────────────────────────────────────────────
// Owns the save boundary for "which keys does that credential name resolve
// to": the credential list for the active backend kind, and the create form.

// New-credential sub-form fields, superset across kinds — only the fields
// relevant to the active backend's credential kind are ever rendered.
interface CredentialDraft {
  name: string
  accessKeyId: string
  secretAccessKey: string
  serviceAccountJSON: string
  accountName: string
  accountKey: string
}

function emptyCredentialDraft(): CredentialDraft {
  return { name: '', accessKeyId: '', secretAccessKey: '', serviceAccountJSON: '', accountName: '', accountKey: '' }
}

interface StorageCredentialsSectionProps {
  backend: StorageBackendType
  activeCredentialKind: CredentialKind
  backendCredentials: Credential[]
  credentialRef: string
  setCredentialRef: (v: string) => void
}

function StorageCredentialsSection({
  backend, activeCredentialKind, backendCredentials, credentialRef, setCredentialRef,
}: StorageCredentialsSectionProps) {
  const createSystemCredential = useCreateSystemCredential()
  const deleteSystemCredential = useDeleteSystemCredential()
  const [credentialDraft, setCredentialDraft] = useState<CredentialDraft>(emptyCredentialDraft())
  const [credentialError, setCredentialError] = useState('')
  const [deleteCredentialTarget, setDeleteCredentialTarget] = useState<string | null>(null)

  const canCreateCredential = (() => {
    if (!credentialDraft.name.trim()) return false
    switch (activeCredentialKind) {
      case 's3':    return !!(credentialDraft.accessKeyId.trim() && credentialDraft.secretAccessKey.trim())
      case 'gcs':   return !!credentialDraft.serviceAccountJSON.trim()
      case 'azure': return !!(credentialDraft.accountName.trim() && credentialDraft.accountKey.trim())
      default:      return false
    }
  })()

  async function handleCreateCredential() {
    setCredentialError('')
    try {
      const data: Record<string, string> =
        activeCredentialKind === 's3'
          ? { access_key_id: credentialDraft.accessKeyId.trim(), secret_access_key: credentialDraft.secretAccessKey.trim() }
          : activeCredentialKind === 'gcs'
            ? { service_account_json: credentialDraft.serviceAccountJSON.trim() }
            : { account_name: credentialDraft.accountName.trim(), account_key: credentialDraft.accountKey.trim() }
      await createSystemCredential.mutateAsync({ name: credentialDraft.name.trim(), kind: activeCredentialKind, data })
      setCredentialDraft(emptyCredentialDraft())
    } catch (err) {
      setCredentialError(err instanceof Error ? err.message : String(err))
    }
  }

  async function confirmDeleteCredential() {
    if (!deleteCredentialTarget) return
    await deleteSystemCredential.mutateAsync(deleteCredentialTarget)
    if (credentialRef === deleteCredentialTarget) setCredentialRef('')
    setDeleteCredentialTarget(null)
  }

  return (
    <>
      <DataBodyTemplate.Group
        layout="stacked"
        title={<>System {BACKEND_LABELS[backend]} Credentials<InstanceScopedBadge /></>}
        description="Access keys for the artifact store, referenced by the Credential field above. Values are write-only."
      >
        {backendCredentials.length > 0 && (
          <div className="space-y-1">
            {backendCredentials.map(c => (
              <div key={c.name} className="flex items-center justify-between rounded-md border border-border px-3 py-2">
                <span className="font-mono text-sm">{c.name}</span>
                <div className="flex items-center gap-2">
                  {credentialRef === c.name && <Badge variant="secondary">in use</Badge>}
                  <IconButton
                    icon={<Trash2 />}
                    label="Delete"
                    onClick={() => setDeleteCredentialTarget(c.name)}
                    className="text-muted-foreground hover:text-destructive"
                  />
                </div>
              </div>
            ))}
          </div>
        )}

        <div className="max-w-xl space-y-3 pt-2">
          <DataBodyTemplate.Row label="Name">
            <Input
              value={credentialDraft.name}
              onChange={e => setCredentialDraft(prev => ({ ...prev, name: e.target.value }))}
              placeholder={`${backend}-artifacts`}
              className="font-mono"
            />
          </DataBodyTemplate.Row>

          {activeCredentialKind === 's3' && (
            <>
              <DataBodyTemplate.Row label="access_key_id">
                <Input
                  value={credentialDraft.accessKeyId}
                  onChange={e => setCredentialDraft(prev => ({ ...prev, accessKeyId: e.target.value }))}
                  className="font-mono text-sm"
                />
              </DataBodyTemplate.Row>
              <DataBodyTemplate.Row label="secret_access_key">
                <Input
                  type="password"
                  value={credentialDraft.secretAccessKey}
                  onChange={e => setCredentialDraft(prev => ({ ...prev, secretAccessKey: e.target.value }))}
                  className="font-mono text-sm"
                />
              </DataBodyTemplate.Row>
            </>
          )}

          {activeCredentialKind === 'gcs' && (
            <DataBodyTemplate.Row label="service_account_json" description="Paste the full service-account JSON key file content.">
              <textarea
                value={credentialDraft.serviceAccountJSON}
                onChange={e => setCredentialDraft(prev => ({ ...prev, serviceAccountJSON: e.target.value }))}
                rows={6}
                placeholder='{"type": "service_account", ...}'
                className="w-full rounded-md border border-input bg-background p-2 font-mono text-xs"
              />
            </DataBodyTemplate.Row>
          )}

          {activeCredentialKind === 'azure' && (
            <>
              <DataBodyTemplate.Row label="account_name">
                <Input
                  value={credentialDraft.accountName}
                  onChange={e => setCredentialDraft(prev => ({ ...prev, accountName: e.target.value }))}
                  className="font-mono text-sm"
                />
              </DataBodyTemplate.Row>
              <DataBodyTemplate.Row label="account_key">
                <Input
                  type="password"
                  value={credentialDraft.accountKey}
                  onChange={e => setCredentialDraft(prev => ({ ...prev, accountKey: e.target.value }))}
                  className="font-mono text-sm"
                />
              </DataBodyTemplate.Row>
            </>
          )}

          {credentialError && <p className="text-sm text-destructive">{credentialError}</p>}
          <div className="flex justify-end pt-2">
            <Button
              size="sm"
              onClick={() => void handleCreateCredential()}
              disabled={!canCreateCredential || createSystemCredential.isPending}
            >
              {createSystemCredential.isPending ? 'Creating…' : `Add ${activeCredentialKind} Credential`}
            </Button>
          </div>
        </div>
      </DataBodyTemplate.Group>

      <AlertDialog open={deleteCredentialTarget != null} onOpenChange={open => { if (!open) setDeleteCredentialTarget(null) }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this system credential?</AlertDialogTitle>
            <AlertDialogDescription>
              &quot;{deleteCredentialTarget}&quot; will be permanently deleted.
              {credentialRef === deleteCredentialTarget && ' It is currently referenced by storage.credentialRef.'}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={deleteSystemCredential.isPending}
              onClick={() => void confirmDeleteCredential()}
            >
              {deleteSystemCredential.isPending ? 'Deleting…' : 'Delete credential'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

// ── Upload Object ───────────────────────────────────────────────────────────
// A narrowly-scoped value-collection action (file + optional key), not an
// entity worth its own page — matches the Modal destination in the
// form-workflow contract, and mirrors credentials' TestCredentialDialog/
// RotateCredentialDialog. Triggered from Uploaded Objects' own toolbar since
// it's that list's create action, not a permanent fixture above the list.

function UploadObjectDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const uploadObject = useUploadObject()
  const [uploadKey, setUploadKey] = useState('')
  const [uploadFile, setUploadFile] = useState<File | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  function handleOpenChange(next: boolean) {
    if (!next) {
      setUploadKey('')
      setUploadFile(null)
      uploadObject.reset()
      if (fileInputRef.current) fileInputRef.current.value = ''
    }
    onOpenChange(next)
  }

  async function handleUpload() {
    if (!uploadFile) return
    try {
      await uploadObject.mutateAsync({ file: uploadFile, key: uploadKey.trim() || uploadFile.name })
      handleOpenChange(false)
    } catch {
      // surfaced below via uploadObject.error
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Upload Object</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <FormField
            label="Object key"
            htmlFor="upload-object-key"
            helperText="Leave empty to use the selected file name."
          >
            <Input
              id="upload-object-key"
              value={uploadKey}
              onChange={e => setUploadKey(e.target.value)}
              placeholder="runs/run-123/model/model.bin"
            />
          </FormField>

          <FormField label="File" htmlFor="upload-object-file">
            <Input
              ref={fileInputRef}
              id="upload-object-file"
              type="file"
              className="hidden"
              onChange={e => setUploadFile(e.target.files?.[0] ?? null)}
            />
            <div className="flex items-center gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => fileInputRef.current?.click()}
              >
                <FolderOpen className="mr-2 size-4" />
                Choose file
              </Button>
              <span className="text-sm text-muted-foreground">
                {uploadFile ? uploadFile.name : 'No file chosen'}
              </span>
            </div>
          </FormField>

          {uploadObject.isError && (
            <p className="text-sm text-destructive">
              {uploadObject.error instanceof Error ? uploadObject.error.message : 'Upload failed.'}
            </p>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => handleOpenChange(false)}>Cancel</Button>
          <Button onClick={() => void handleUpload()} disabled={!uploadFile || uploadObject.isPending}>
            <Save className="mr-2 size-4" />
            {uploadObject.isPending ? 'Uploading…' : 'Upload'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ── Uploaded Objects ────────────────────────────────────────────────────────
// Folder-tree browser over this project's uploads/ prefix, with delete as its
// only mutation. Per managed-table.md, list content lives in
// DataBodyTemplate.Resource (toolbar/notice/pagination), not Group — Group is
// reserved for the form-workflow save boundaries above (Config, Credentials).
//
// Listing is one level at a time (S3 ListObjectsV2 Delimiter="/" semantics —
// see storage.Store.List on the Go side), the same convention the AWS/MinIO
// consoles use: a folder row is a pseudo-directory aggregated server-side,
// not a real DataGrid nesting, and drilling in means re-querying with that
// folder's own key as the new prefix. Root Label is what breadcrumb segment
// 0 always reads.

const OBJECTS_PAGE_SIZE = 20
const ROOT_LABEL = 'uploads'

function breadcrumbSegments(prefix: string): string[] {
  return prefix.split('/').filter(Boolean)
}

function UploadedObjectsSection({ projectId }: { projectId: string }) {
  const { open } = useSidePanel()
  const [appliedPrefix, setAppliedPrefix] = useState('')
  const [pageIndex, setPageIndex] = useState(0)
  const [nameFilter, setNameFilter] = useState('')
  const objectsQuery = useStorageObjectsPaged(OBJECTS_PAGE_SIZE, pageIndex * OBJECTS_PAGE_SIZE, appliedPrefix)
  const objects = objectsQuery.data?.objects ?? []
  const total = objectsQuery.data?.total ?? 0
  // Filters only the current page/folder level — matches the accepted
  // trade-off in CredentialsPage's kind filter. A true cross-page name
  // search would need server-side support object storage doesn't offer.
  const filteredObjects = nameFilter.trim()
    ? objects.filter(o => o.key.slice(appliedPrefix.length).toLowerCase().includes(nameFilter.trim().toLowerCase()))
    : objects
  const deleteObject = useDeleteObject()
  const [deleteObjectTarget, setDeleteObjectTarget] = useState<string | null>(null)
  const [uploadOpen, setUploadOpen] = useState(false)

  function navigateTo(nextPrefix: string) {
    setAppliedPrefix(nextPrefix)
    setPageIndex(0)
  }

  function confirmDeleteObject() {
    if (!deleteObjectTarget) return
    deleteObject.mutate(deleteObjectTarget)
    setDeleteObjectTarget(null)
  }

  const segments = breadcrumbSegments(appliedPrefix)

  const objectColumns: DataGridColumnDef<StorageObjectInfo>[] = [
    {
      accessorKey: 'key',
      header: 'Name',
      meta: { minWidth: 240, flex: 1 },
      cell: ({ row }) => {
        const leaf = row.original.key.slice(appliedPrefix.length).replace(/\/$/, '')
        return row.original.is_dir ? (
          <span className="flex items-center gap-2 font-medium">
            <Folder className="size-4 shrink-0 text-muted-foreground" />
            {leaf}
          </span>
        ) : (
          <span className="block truncate font-mono text-xs" title={row.original.key}>
            {leaf}
          </span>
        )
      },
    },
    {
      accessorKey: 'size',
      header: 'Size',
      meta: { minWidth: 90 },
      cell: ({ row }) => (
        <span className="text-xs text-muted-foreground">
          {row.original.is_dir ? '—' : fmtBytes(row.original.size)}
        </span>
      ),
    },
    {
      accessorKey: 'modified_at',
      header: 'Modified',
      meta: { minWidth: 160 },
      cell: ({ row }) => (
        <span className="text-xs text-muted-foreground">
          {row.original.is_dir ? '—' : fmtDate(row.original.modified_at)}
        </span>
      ),
    },
    {
      id: 'actions',
      header: '',
      meta: { minWidth: 80, align: 'right' },
      cell: ({ row }) => row.original.is_dir ? null : (
        <RowActions>
          <IconButton
            icon={<Download />}
            label="Download"
            onClick={e => {
              e.stopPropagation()
              window.open(storageObjectURL(projectId, row.original.key), '_blank', 'noopener,noreferrer')
            }}
          />
          <IconButton
            icon={<Trash2 />}
            label="Delete"
            disabled={deleteObject.isPending && deleteObject.variables === row.original.key}
            onClick={e => { e.stopPropagation(); setDeleteObjectTarget(row.original.key) }}
            className="text-destructive hover:bg-destructive/10"
          />
        </RowActions>
      ),
    },
  ]

  return (
    <>
      <div className="mb-3 flex items-center gap-2 text-sm font-medium text-foreground">
        <Folder className="size-4 shrink-0 text-muted-foreground" />
        <Breadcrumb>
          <BreadcrumbList>
            <BreadcrumbItem>
              {segments.length === 0 ? (
                <BreadcrumbPage>{ROOT_LABEL}</BreadcrumbPage>
              ) : (
                <BreadcrumbLink href="#" onClick={e => { e.preventDefault(); navigateTo('') }}>
                  {ROOT_LABEL}
                </BreadcrumbLink>
              )}
            </BreadcrumbItem>
            {segments.map((segment, i) => {
              const segmentPrefix = segments.slice(0, i + 1).join('/') + '/'
              const isLast = i === segments.length - 1
              return (
                <Fragment key={segmentPrefix}>
                  <BreadcrumbSeparator />
                  <BreadcrumbItem>
                    {isLast ? (
                      <BreadcrumbPage>{segment}</BreadcrumbPage>
                    ) : (
                      <BreadcrumbLink href="#" onClick={e => { e.preventDefault(); navigateTo(segmentPrefix) }}>
                        {segment}
                      </BreadcrumbLink>
                    )}
                  </BreadcrumbItem>
                </Fragment>
              )
            })}
          </BreadcrumbList>
        </Breadcrumb>
      </div>

      <DataBodyTemplate.Resource
        toolbarLeft={
          <div className="w-52">
            <FilterInput
              config={{
                key: 'objectSearch',
                type: 'text',
                placeholder: 'Search objects…',
                display: { size: 'sm', leadingIcon: <Search /> },
              }}
              value={nameFilter}
              onChange={v => setNameFilter(typeof v === 'string' ? v : '')}
            />
          </div>
        }
        toolbarRight={
          <>
            <Button
              variant="outline"
              size="sm"
              onClick={() => void objectsQuery.refetch()}
              disabled={objectsQuery.isFetching}
            >
              <RefreshCw className={objectsQuery.isFetching ? 'size-4 animate-spin' : 'size-4'} />
            </Button>
            <Button size="sm" onClick={() => setUploadOpen(true)}>
              <Plus className="mr-1.5 size-3.5" />
              Upload
            </Button>
          </>
        }
        notice={objectsQuery.isError && (
          <QueryErrorNotice
            message="Failed to load uploaded objects"
            error={objectsQuery.error}
            onRetry={() => void objectsQuery.refetch()}
          />
        )}
      >
        <DataGrid
          data={filteredObjects}
          columns={objectColumns}
          isLoading={objectsQuery.isPending}
          emptyMessage={objectsQuery.isError ? undefined : (segments.length > 0 ? 'This folder is empty.' : 'No uploaded objects yet.')}
          tableWidthMode="fill-last"
          rowHeight={44}
          rowCursor
          onRowClick={(object) => {
            if (object.is_dir) { navigateTo(object.key); return }
            open(
              <ObjectDetailPanel
                projectId={projectId}
                object={object}
                onDelete={o => setDeleteObjectTarget(o.key)}
              />,
              { size: 480 },
            )
          }}
          classNames={{ footer: 'pt-3' }}
          pagination={{
            pageSize: OBJECTS_PAGE_SIZE,
            pageIndex,
            pageCount: Math.max(1, Math.ceil(total / OBJECTS_PAGE_SIZE)),
            onPageChange: setPageIndex,
          }}
          footer={(table) => <DataGridPaginationBar table={table} totalCount={total} />}
        />
      </DataBodyTemplate.Resource>

      <UploadObjectDialog open={uploadOpen} onOpenChange={setUploadOpen} />

      <AlertDialog open={deleteObjectTarget != null} onOpenChange={open => { if (!open) setDeleteObjectTarget(null) }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this object?</AlertDialogTitle>
            <AlertDialogDescription>
              &quot;{deleteObjectTarget}&quot; will be permanently deleted from the object store.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={confirmDeleteObject}>
              Delete object
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

// ── route component ─────────────────────────────────────────────────────────
// Owns only the page template, breadcrumb/title, and the state genuinely
// shared across save boundaries: which backend is selected and which
// credential it points at (Artifact Store Config reads and writes both;
// System Credentials reads both to filter its list and clear a deleted ref).

const DEFAULT_TAB = 'objects'

function StoragePageInner() {
  const projectId = useProjectId()
  const settingsQuery = useStorageSettings()
  const storage = settingsQuery.data ?? null

  const [searchParams, setSearchParams] = useSearchParams()
  const activeTab = searchParams.get('tab') ?? DEFAULT_TAB

  function handleTabChange(next: string) {
    setSearchParams({ ...Object.fromEntries(searchParams), tab: next }, { replace: true })
  }

  const [disabled, setDisabled] = useState(false)
  const [token, setToken] = useState('')
  const [credentialRef, setCredentialRef] = useState('')
  const [backendForm, setBackendForm] = useState<BackendForm>(emptyBackendForm())
  const formInitialized = useRef(false)

  const { data: systemCredentials = [] } = useSystemCredentials()
  const activeCredentialKind = BACKEND_CREDENTIAL_KIND[backendForm.backend]
  const backendCredentials = systemCredentials.filter(c => c.kind === activeCredentialKind && !c.disabled)

  useEffect(() => {
    if (storage?.config && !formInitialized.current) {
      formInitialized.current = true
      setDisabled(storage.config.disabled)
      setToken(storage.config.token)
      setCredentialRef(storage.config.credentialRef ?? '')
      setBackendForm(parseStorageURL(storage.config.url))
    }
  }, [storage])

  const status = storage?.effective.status ?? 'disabled'
  const restartRequired = storage?.restart_required ?? false

  return (
    <DataBodyTemplate
      title="Storage"
      activeTab={activeTab}
      onTabChange={handleTabChange}
      status={
        settingsQuery.isSuccess && (
          <>
            <Badge variant={statusVariant(status)}>{status}</Badge>
            {restartRequired && <Badge variant="outline">Restart required</Badge>}
          </>
        )
      }
    >
      <DataBodyTemplate.Tab id="objects" label="Objects">
        <UploadedObjectsSection projectId={projectId} />
      </DataBodyTemplate.Tab>

      <DataBodyTemplate.Tab id="config" label="Configuration">
        <ArtifactStoreConfigSection
          storage={storage}
          isLoading={settingsQuery.isPending}
          loadError={settingsQuery.error}
          disabled={disabled}
          setDisabled={setDisabled}
          token={token}
          setToken={setToken}
          credentialRef={credentialRef}
          setCredentialRef={setCredentialRef}
          backendForm={backendForm}
          setBackendForm={setBackendForm}
          activeCredentialKind={activeCredentialKind}
          backendCredentials={backendCredentials}
        />

        {activeCredentialKind && (
          <StorageCredentialsSection
            backend={backendForm.backend}
            activeCredentialKind={activeCredentialKind}
            backendCredentials={backendCredentials}
            credentialRef={credentialRef}
            setCredentialRef={setCredentialRef}
          />
        )}
      </DataBodyTemplate.Tab>
    </DataBodyTemplate>
  )
}

export default function StoragePage() {
  return (
    <SidePanelProvider defaultSize={480} defaultMinSize={380} defaultMaxSize={800}>
      <StoragePageInner />
    </SidePanelProvider>
  )
}
