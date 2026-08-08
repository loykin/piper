import { useEffect, useRef, useState } from 'react'
import { useProjectId } from '@/lib/projectContext'
import { Download, FolderOpen, RefreshCw, Save, Trash2 } from 'lucide-react'
import { DataBodyTemplate, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@loykin/designkit'
import { DataGrid, type DataGridColumnDef } from '@loykin/gridkit'
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
import { Badge } from '@/components/ui/badge'
import { Button, buttonVariants } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  useSystemCredentials,
  useCreateSystemCredential,
  useDeleteSystemCredential,
} from '@/features/credentials/hooks'
import {
  useStorageSettings,
  useStorageObjects,
  useSaveStorageSettings,
  useDeleteObject,
  useUploadObject,
} from '@/features/storage/hooks'
import { storageObjectURL, type StorageObjectInfo, type StorageSettingsView } from '@/features/storage/api'

function statusVariant(status: StorageSettingsView['effective']['status']): 'default' | 'secondary' | 'destructive' {
  switch (status) {
    case 'enabled':     return 'default'
    case 'unavailable': return 'destructive'
    default:            return 'secondary'
  }
}

function fmtBytes(size: number): string {
  if (!Number.isFinite(size) || size < 0) return '—'
  if (size < 1024) return `${size} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let value = size / 1024
  let unit = units[0]
  for (let i = 0; i < units.length; i += 1) {
    unit = units[i]
    if (value < 1024 || i === units.length - 1) break
    value /= 1024
  }
  return `${value.toFixed(value >= 10 ? 0 : 1)} ${unit}`
}

function fmtDate(value: string): string {
  if (!value || value.startsWith('0001-01-01')) return '—'
  const ts = new Date(value)
  if (Number.isNaN(ts.getTime())) return '—'
  return ts.toLocaleString()
}

export default function StoragePage() {
  const projectId = useProjectId()
  const settingsQuery = useStorageSettings()
  const storage = settingsQuery.data ?? null
  const [prefix, setPrefix] = useState('')
  const [appliedPrefix, setAppliedPrefix] = useState('')
  const objectsQuery = useStorageObjects(appliedPrefix)
  const objects = objectsQuery.data ?? []
  const saveSettings = useSaveStorageSettings()
  const deleteObject = useDeleteObject()
  const uploadObject = useUploadObject()

  const [uploadKey, setUploadKey] = useState('')
  const [uploadFile, setUploadFile] = useState<File | null>(null)
  const [form, setForm] = useState({ disabled: false, url: '', token: '', credentialRef: '' })
  const [deleteObjectTarget, setDeleteObjectTarget] = useState<string | null>(null)
  const [deleteCredentialTarget, setDeleteCredentialTarget] = useState<string | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const formInitialized = useRef(false)

  const { data: systemCredentials = [] } = useSystemCredentials()
  const s3Credentials = systemCredentials.filter(c => c.kind === 's3' && !c.disabled)
  const createSystemCredential = useCreateSystemCredential()
  const deleteSystemCredential = useDeleteSystemCredential()
  const [s3Form, setS3Form] = useState({ name: '', accessKeyId: '', secretAccessKey: '' })
  const [s3Error, setS3Error] = useState('')

  const canCreateS3 = s3Form.name.trim() && s3Form.accessKeyId.trim() && s3Form.secretAccessKey.trim()

  useEffect(() => {
    if (storage?.config && !formInitialized.current) {
      formInitialized.current = true
      setForm({ ...storage.config, credentialRef: storage.config.credentialRef ?? '' })
    }
  }, [storage])

  async function handleCreateS3Credential() {
    setS3Error('')
    try {
      await createSystemCredential.mutateAsync({
        name: s3Form.name.trim(),
        kind: 's3',
        data: {
          access_key_id: s3Form.accessKeyId.trim(),
          secret_access_key: s3Form.secretAccessKey.trim(),
        },
      })
      setS3Form({ name: '', accessKeyId: '', secretAccessKey: '' })
    } catch (err) {
      setS3Error(err instanceof Error ? err.message : String(err))
    }
  }

  async function confirmDeleteS3Credential() {
    if (!deleteCredentialTarget) return
    await deleteSystemCredential.mutateAsync(deleteCredentialTarget)
    if (form.credentialRef === deleteCredentialTarget) setForm(prev => ({ ...prev, credentialRef: '' }))
    setDeleteCredentialTarget(null)
  }

  const enabled = storage?.effective.status === 'enabled'
  const status   = storage?.effective.status ?? 'disabled'
  const backend  = storage?.effective.backend || '—'
  const restartRequired = storage?.restart_required ?? false

  function handleSave() {
    saveSettings.mutate(
      {
        disabled: form.disabled,
        url: form.url.trim(),
        token: form.token,
        credentialRef: form.credentialRef.trim() || undefined,
      },
      {
        onSuccess: (next) => setForm({ ...next.config, credentialRef: next.config.credentialRef ?? '' }),
      },
    )
  }

  function confirmDeleteObject() {
    if (!deleteObjectTarget) return
    deleteObject.mutate(deleteObjectTarget)
    setDeleteObjectTarget(null)
  }

  async function handleUpload() {
    if (!uploadFile) return
    await uploadObject.mutateAsync({ file: uploadFile, key: uploadKey.trim() || uploadFile.name })
    setUploadFile(null)
    setUploadKey('')
    if (fileInputRef.current) fileInputRef.current.value = ''
  }

  const objectColumns: DataGridColumnDef<StorageObjectInfo>[] = [
    {
      accessorKey: 'key',
      header: 'Key',
      meta: { minWidth: 240, flex: 1 },
      cell: ({ row }) => (
        <span className="block truncate font-mono text-xs" title={row.original.key}>
          {row.original.key}
        </span>
      ),
    },
    {
      accessorKey: 'size',
      header: 'Size',
      meta: { minWidth: 90 },
      cell: ({ row }) => (
        <span className="text-xs text-muted-foreground">{fmtBytes(row.original.size)}</span>
      ),
    },
    {
      accessorKey: 'modified_at',
      header: 'Modified',
      meta: { minWidth: 160 },
      cell: ({ row }) => (
        <span className="text-xs text-muted-foreground">{fmtDate(row.original.modified_at)}</span>
      ),
    },
    {
      id: 'actions',
      header: '',
      meta: { minWidth: 160, align: 'right' },
      cell: ({ row }) => (
        <div className="flex justify-end gap-2">
          <a
            href={storageObjectURL(projectId, row.original.key)}
            target="_blank"
            rel="noreferrer"
            className={buttonVariants({ variant: 'outline', size: 'sm' })}
            onClick={e => e.stopPropagation()}
          >
            <Download className="mr-2 size-4" />
            Download
          </a>
          <IconButton
            icon={<Trash2 />}
            label="Delete"
            disabled={deleteObject.isPending && deleteObject.variables === row.original.key}
            onClick={e => { e.stopPropagation(); setDeleteObjectTarget(row.original.key) }}
            className="text-destructive hover:bg-destructive/10"
          />
        </div>
      ),
    },
  ]

  if (settingsQuery.isPending) {
    return (
      <DataBodyTemplate title="Storage">
        <DataBodyTemplate.Body>
          <p className="text-sm text-muted-foreground">Loading…</p>
        </DataBodyTemplate.Body>
      </DataBodyTemplate>
    )
  }

  return (
    <>
    <DataBodyTemplate
      title="Storage"
      description="Manage artifact storage configuration and browse stored objects."
      status={
        <>
          <Badge variant={statusVariant(status)}>{status}</Badge>
          {restartRequired && <Badge variant="outline">Restart required</Badge>}
        </>
      }
      actions={
        <Button size="sm" onClick={handleSave} disabled={saveSettings.isPending || !storage}>
          <Save className="mr-2 size-4" />
          {saveSettings.isPending ? 'Saving…' : 'Save'}
        </Button>
      }
    >
      <DataBodyTemplate.Body>

        <DataBodyTemplate.Group layout="stacked" title="Artifact Store Config">
          <DataBodyTemplate.Row
            label="Enabled"
            description="Disabled hides object store exports and downloads after restart."
          >
            <Switch
              checked={!form.disabled}
              onCheckedChange={(checked) => setForm(prev => ({ ...prev, disabled: !checked }))}
            />
          </DataBodyTemplate.Row>

          <DataBodyTemplate.Row
            label="storage.url"
            description="Leave empty to use the server default artifact store."
          >
            <Input
              value={form.url}
              onChange={e => setForm(prev => ({ ...prev, url: e.target.value }))}
              placeholder="s3://bucket?endpoint=http://localhost:9000..."
            />
          </DataBodyTemplate.Row>

          <DataBodyTemplate.Row label="storage.token">
            <Input
              value={form.token}
              onChange={e => setForm(prev => ({ ...prev, token: e.target.value }))}
              placeholder="Bearer token for HTTP stores"
            />
          </DataBodyTemplate.Row>

          <DataBodyTemplate.Row
            label="storage.credentialRef"
            description="System s3 credential supplying access keys for an s3:// URL. The URL carries only bucket/endpoint/region."
          >
            <Select
              value={form.credentialRef || '__none__'}
              onValueChange={v => setForm(prev => ({ ...prev, credentialRef: v === '__none__' ? '' : (v ?? '') }))}
            >
              <SelectTrigger className="w-72"><SelectValue placeholder="None (keys in URL)" /></SelectTrigger>
              <SelectContent>
                <SelectItem value="__none__">None (keys in URL)</SelectItem>
                {s3Credentials.map(c => (
                  <SelectItem key={c.name} value={c.name}>{c.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </DataBodyTemplate.Row>

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
        </DataBodyTemplate.Group>

        <DataBodyTemplate.Group layout="stacked" title="System S3 Credentials" description="Access keys for the artifact store, referenced by storage.credentialRef. Values are write-only.">
          {s3Credentials.length > 0 && (
            <div className="space-y-1">
              {s3Credentials.map(c => (
                <div key={c.name} className="flex items-center justify-between rounded-md border border-border px-3 py-2">
                  <span className="font-mono text-sm">{c.name}</span>
                  <div className="flex items-center gap-2">
                    {form.credentialRef === c.name && <Badge variant="secondary">in use</Badge>}
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
          <div className="grid max-w-xl gap-3 pt-2">
            <div className="space-y-1.5">
              <Label htmlFor="s3-name">Name</Label>
              <Input
                id="s3-name"
                value={s3Form.name}
                onChange={e => setS3Form(prev => ({ ...prev, name: e.target.value }))}
                placeholder="minio-artifacts"
                className="font-mono"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="s3-access">access_key_id</Label>
              <Input
                id="s3-access"
                value={s3Form.accessKeyId}
                onChange={e => setS3Form(prev => ({ ...prev, accessKeyId: e.target.value }))}
                className="font-mono text-sm"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="s3-secret">secret_access_key</Label>
              <Input
                id="s3-secret"
                type="password"
                value={s3Form.secretAccessKey}
                onChange={e => setS3Form(prev => ({ ...prev, secretAccessKey: e.target.value }))}
                className="font-mono text-sm"
              />
            </div>
            {s3Error && <p className="text-sm text-destructive">{s3Error}</p>}
            <div>
              <Button
                size="sm"
                onClick={() => void handleCreateS3Credential()}
                disabled={!canCreateS3 || createSystemCredential.isPending}
              >
                {createSystemCredential.isPending ? 'Creating…' : 'Add S3 Credential'}
              </Button>
            </div>
          </div>
        </DataBodyTemplate.Group>

        <DataBodyTemplate.Group layout="stacked" title="Upload Object">
          <DataBodyTemplate.Row
            label="Object key"
            description="Leave empty to use the selected file name."
          >
            <Input
              value={uploadKey}
              onChange={e => setUploadKey(e.target.value)}
              placeholder="runs/run-123/model/model.bin"
              disabled={!enabled}
            />
          </DataBodyTemplate.Row>

          <DataBodyTemplate.Row label="File">
            <Input
              ref={fileInputRef}
              type="file"
              className="hidden"
              onChange={e => setUploadFile(e.target.files?.[0] ?? null)}
              disabled={!enabled}
            />
            <div className="flex items-center gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={!enabled}
                onClick={() => fileInputRef.current?.click()}
              >
                <FolderOpen className="mr-2 size-4" />
                Choose file
              </Button>
              <span className="text-sm text-muted-foreground">
                {uploadFile ? uploadFile.name : 'No file chosen'}
              </span>
            </div>
          </DataBodyTemplate.Row>

          <DataBodyTemplate.Field label="">
            <Button
              size="sm"
              onClick={() => void handleUpload()}
              disabled={!enabled || !uploadFile || uploadObject.isPending}
            >
              <Save className="mr-2 size-4" />
              {uploadObject.isPending ? 'Uploading…' : 'Upload'}
            </Button>
          </DataBodyTemplate.Field>
        </DataBodyTemplate.Group>

        <DataBodyTemplate.Group
          layout="stacked"
          title="Uploaded Objects"
          description="Files uploaded directly through this page, under this project's uploads/ prefix. Pipeline run artifacts are stored separately — browse and download them from that run's detail page."
          actions={
            <div className="flex items-center gap-2">
              <Input
                value={prefix}
                onChange={e => setPrefix(e.target.value)}
                placeholder="prefix filter"
                className="w-52"
                onKeyDown={(e) => {
                  if (e.key === 'Enter') { e.preventDefault(); setAppliedPrefix(prefix) }
                }}
              />
              <Button
                variant="outline"
                size="sm"
                onClick={() => (appliedPrefix === prefix ? void objectsQuery.refetch() : setAppliedPrefix(prefix))}
                disabled={objectsQuery.isFetching || !enabled}
              >
                <RefreshCw className={objectsQuery.isFetching ? 'size-4 animate-spin' : 'size-4'} />
              </Button>
            </div>
          }
        >
          <DataGrid
            data={objects}
            columns={objectColumns}
            emptyMessage={
              !enabled
                ? 'Object storage is disabled or unavailable.'
                : 'No uploaded objects found for this prefix.'
            }
            tableWidthMode="fill-last"
            rowHeight={44}
          />
        </DataBodyTemplate.Group>

      </DataBodyTemplate.Body>
    </DataBodyTemplate>

    <AlertDialog open={deleteObjectTarget != null} onOpenChange={open => { if (!open) setDeleteObjectTarget(null) }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete this object?</AlertDialogTitle>
          <AlertDialogDescription>
            "{deleteObjectTarget}" will be permanently deleted from the object store.
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

    <AlertDialog open={deleteCredentialTarget != null} onOpenChange={open => { if (!open) setDeleteCredentialTarget(null) }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete this system credential?</AlertDialogTitle>
          <AlertDialogDescription>
            "{deleteCredentialTarget}" will be permanently deleted.
            {form.credentialRef === deleteCredentialTarget && ' It is currently referenced by storage.credentialRef.'}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={deleteSystemCredential.isPending}
            onClick={() => void confirmDeleteS3Credential()}
          >
            {deleteSystemCredential.isPending ? 'Deleting…' : 'Delete credential'}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
    </>
  )
}
