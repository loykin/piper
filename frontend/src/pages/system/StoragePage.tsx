import { Fragment, useRef, useState } from 'react'
import { useProjectId } from '@/lib/projectContext'
import { useSearchParams } from '@/lib/router'
import { ChevronRight, Download, Folder, FolderOpen, Plus, RefreshCw, Save, Search, Trash2 } from 'lucide-react'
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
  DataBodyTemplate,
  FormField,
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
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import {
  useSystemCredentials,
  useCreateSystemCredential,
  useDeleteSystemCredential,
} from '@/features/credentials/hooks'
import type { Credential, CredentialKind } from '@/features/credentials/types'
import {
  useStorageSettings,
  useStorageObjectsPaged,
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
  parseStorageURL,
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

// ── Artifact Store Config ───────────────────────────────────────────────────
// Read-only diagnostic: the artifact storage backend (bucket/endpoint/
// region/which-backend) is deploy-time-only configuration, the same class of
// setting as runtime.type or the database driver — see storage_admin.go's
// StorageSettingsView doc comment on the Go side for the full rationale.
// Every notebook-volume template snapshot, viewer, from_artifact/run:latest
// resolution, and past run's artifact download that references the current
// backend would go permanently unreachable the moment a live-edited backend
// took effect, with no warning — so this section only ever shows what's
// actually running and what's pending on disk, never an editable form.
// Changing the backend requires editing storage.yaml directly and
// restarting the server. This is the "top = what's actually running
// (read-only)" half of the page; System Credentials below is the "bottom =
// manage stored credential entries (editable)" half.

interface ArtifactStoreConfigSectionProps {
  storage: StorageSettingsView | null
  isLoading: boolean
  loadError: unknown
}

function ArtifactStoreConfigSection({ storage, isLoading, loadError }: ArtifactStoreConfigSectionProps) {
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
  const backendLabel = storage?.effective.backend || '—'
  const cfg = storage?.config
  const pending = parseStorageURL(cfg?.url ?? '')

  return (
    <DataBodyTemplate.Group
      layout="stacked"
      title={<>Artifact Store Config<InstanceScopedBadge /></>}
      description="Read-only. Changing the artifact storage backend requires editing storage.yaml directly on this server and restarting it — the same as runtime.type or the database driver."
    >
      <DataBodyTemplate.Field label="Runtime status" description="What's actually active right now.">
        <div className="space-y-1 text-sm">
          <p><span className="text-muted-foreground">Status: </span>{status}</p>
          <p><span className="text-muted-foreground">Backend: </span>{backendLabel}</p>
          <p><span className="text-muted-foreground">Reason: </span>{storage?.effective.reason || '—'}</p>
        </div>
      </DataBodyTemplate.Field>

      <DataBodyTemplate.Field label="Config file" description="Read from this path on startup.">
        <span className="break-all font-mono text-xs">{storage?.config_path || '—'}</span>
      </DataBodyTemplate.Field>

      <DataBodyTemplate.Field
        label="Pending config"
        description={storage?.restart_required
          ? 'storage.yaml differs from the running configuration — restart the server to apply it.'
          : 'What storage.yaml currently holds. Matches the running configuration.'}
      >
        <div className="space-y-1 text-sm">
          <p><span className="text-muted-foreground">Enabled: </span>{cfg?.disabled ? 'No' : 'Yes'}</p>
          <p><span className="text-muted-foreground">Backend: </span>{BACKEND_LABELS[pending.backend]}</p>
          {pending.backend === 's3' && (
            <>
              <p><span className="text-muted-foreground">Bucket: </span>{pending.bucket || '—'}</p>
              <p><span className="text-muted-foreground">Endpoint: </span>{pending.endpoint || '(AWS S3)'}</p>
              <p><span className="text-muted-foreground">Region: </span>{pending.region || '—'}</p>
              <p><span className="text-muted-foreground">Force path style: </span>{pending.forcePathStyle ? 'Yes' : 'No'}</p>
            </>
          )}
          {(pending.backend === 'gcs' || pending.backend === 'azure') && (
            <p><span className="text-muted-foreground">{pending.backend === 'gcs' ? 'Bucket' : 'Container'}: </span>{pending.bucket || '—'}</p>
          )}
          {pending.backend === 'http' && (
            <>
              <p><span className="text-muted-foreground">Base URL: </span>{pending.httpURL || '—'}</p>
              <p><span className="text-muted-foreground">Bearer token: </span>{cfg?.token ? 'set' : 'not set'}</p>
            </>
          )}
          <p><span className="text-muted-foreground">Credential: </span>{cfg?.credentialRef || 'None'}</p>
        </div>
      </DataBodyTemplate.Field>

      {storage?.restart_required && (
        <Badge variant="outline" className="w-fit">Restart required to apply storage.yaml</Badge>
      )}
    </DataBodyTemplate.Group>
  )
}

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
}

function StorageCredentialsSection({
  backend, activeCredentialKind, backendCredentials, credentialRef,
}: StorageCredentialsSectionProps) {
  const createSystemCredential = useCreateSystemCredential()
  const deleteSystemCredential = useDeleteSystemCredential()
  const [credentialDraft, setCredentialDraft] = useState<CredentialDraft>(emptyCredentialDraft())
  const [nameError, setNameError] = useState('')
  const [credentialError, setCredentialError] = useState('')
  const [deleteCredentialTarget, setDeleteCredentialTarget] = useState<string | null>(null)
  const [addOpen, setAddOpen] = useState(false)

  // Name is validated explicitly on submit (below) rather than folded into
  // this gate, so clicking "Add" with an empty name always surfaces a
  // visible "Name is required." error instead of the button just staying
  // inert with no explanation — mirrors AlertRuleCreatePage's Zod-driven
  // 'Name is required.' error on the same kind of empty-submit.
  const canCreateCredential = (() => {
    switch (activeCredentialKind) {
      case 's3':    return !!(credentialDraft.accessKeyId.trim() && credentialDraft.secretAccessKey.trim())
      case 'gcs':   return !!credentialDraft.serviceAccountJSON.trim()
      case 'azure': return !!(credentialDraft.accountName.trim() && credentialDraft.accountKey.trim())
      default:      return false
    }
  })()

  async function handleCreateCredential() {
    setCredentialError('')
    const trimmedName = credentialDraft.name.trim()
    if (!trimmedName) {
      setNameError('Name is required.')
      return
    }
    setNameError('')
    try {
      const data: Record<string, string> =
        activeCredentialKind === 's3'
          ? { access_key_id: credentialDraft.accessKeyId.trim(), secret_access_key: credentialDraft.secretAccessKey.trim() }
          : activeCredentialKind === 'gcs'
            ? { service_account_json: credentialDraft.serviceAccountJSON.trim() }
            : { account_name: credentialDraft.accountName.trim(), account_key: credentialDraft.accountKey.trim() }
      await createSystemCredential.mutateAsync({ name: trimmedName, kind: activeCredentialKind, data })
      setCredentialDraft(emptyCredentialDraft())
      setAddOpen(false)
    } catch (err) {
      setCredentialError(err instanceof Error ? err.message : String(err))
    }
  }

  function confirmDeleteCredential() {
    if (!deleteCredentialTarget) return
    deleteSystemCredential.mutate(deleteCredentialTarget)
    setDeleteCredentialTarget(null)
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
            <div className="max-w-xl space-y-3 pt-3">
              <FormField label="Name" htmlFor="storage-credential-name" error={nameError}>
                <Input
                  id="storage-credential-name"
                  value={credentialDraft.name}
                  onChange={e => {
                    setCredentialDraft(prev => ({ ...prev, name: e.target.value }))
                    if (nameError) setNameError('')
                  }}
                  placeholder={`${backend}-artifacts`}
                  className="font-mono"
                  aria-invalid={!!nameError}
                />
              </FormField>

              {activeCredentialKind === 's3' && (
                <>
                  <DataBodyTemplate.Row label="access_key_id">
                    <Input
                      aria-label="access_key_id"
                      value={credentialDraft.accessKeyId}
                      onChange={e => setCredentialDraft(prev => ({ ...prev, accessKeyId: e.target.value }))}
                      className="font-mono text-sm"
                    />
                  </DataBodyTemplate.Row>
                  <DataBodyTemplate.Row label="secret_access_key">
                    <Input
                      aria-label="secret_access_key"
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
                    aria-label="service_account_json"
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
                      aria-label="account_name"
                      value={credentialDraft.accountName}
                      onChange={e => setCredentialDraft(prev => ({ ...prev, accountName: e.target.value }))}
                      className="font-mono text-sm"
                    />
                  </DataBodyTemplate.Row>
                  <DataBodyTemplate.Row label="account_key">
                    <Input
                      aria-label="account_key"
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
          </CollapsibleContent>
        </Collapsible>
      </DataBodyTemplate.Group>

      <AlertDialog open={deleteCredentialTarget != null} onOpenChange={open => { if (!open) setDeleteCredentialTarget(null) }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this system credential?</AlertDialogTitle>
            <AlertDialogDescription>
              &quot;{deleteCredentialTarget}&quot; will be permanently deleted.
              {credentialRef === deleteCredentialTarget && ' It is currently referenced by storage.credentialRef — deleting it will make the artifact store unavailable after the next restart until storage.yaml is updated.'}
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

// Mirrors serve.go's maxBlobRequestBodyBytes — the built-in store's blob
// upload cap. Kept as a client-side estimate only; the server response is
// still the source of truth if this ever drifts.
const MAX_UPLOAD_BYTES = 4 * 1024 * 1024 * 1024

function formatBytes(bytes: number): string {
  if (bytes >= 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`
  if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / 1024).toFixed(1)} KB`
}

function UploadObjectDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const uploadObject = useUploadObject()
  const [uploadKey, setUploadKey] = useState('')
  const [uploadFile, setUploadFile] = useState<File | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const fileTooLarge = uploadFile !== null && uploadFile.size > MAX_UPLOAD_BYTES

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

          {fileTooLarge && uploadFile && (
            <p className="text-sm text-destructive">
              {formatBytes(uploadFile.size)} exceeds the {formatBytes(MAX_UPLOAD_BYTES)} upload limit.
            </p>
          )}
          {uploadObject.isError && (
            <p className="text-sm text-destructive">
              {uploadObject.error instanceof Error ? uploadObject.error.message : 'Upload failed.'}
            </p>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => handleOpenChange(false)}>Cancel</Button>
          <Button onClick={() => void handleUpload()} disabled={!uploadFile || fileTooLarge || uploadObject.isPending}>
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
// shared across the two Configuration-tab sections: which backend is
// pending in storage.yaml and which credential it names. Both are derived
// read-only from the settings query now — Artifact Store Config no longer
// has editable state to own, and System Credentials only reads this to
// filter its list and label the "in use" credential.

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

  const backend = parseStorageURL(storage?.config.url ?? '').backend
  const credentialRef = storage?.config.credentialRef ?? ''

  const { data: systemCredentials = [] } = useSystemCredentials()
  const activeCredentialKind = BACKEND_CREDENTIAL_KIND[backend]
  const backendCredentials = systemCredentials.filter(c => c.kind === activeCredentialKind && !c.disabled)

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
        />

        {activeCredentialKind && (
          <StorageCredentialsSection
            backend={backend}
            activeCredentialKind={activeCredentialKind}
            backendCredentials={backendCredentials}
            credentialRef={credentialRef}
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
