import { useEffect, useRef, useState } from 'react'
import { useProjectId } from '@/lib/projectContext'
import { Download, FolderOpen, RefreshCw, Save, Trash2 } from 'lucide-react'
import { DataBodyTemplate } from '@loykin/designkit'
import { DataGrid, type DataGridColumnDef } from '@loykin/gridkit'
import { Badge } from '@/components/ui/badge'
import { Button, buttonVariants } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import {
  deleteStorageObject,
  getStorageSettings,
  listStorageObjects,
  saveStorageSettings,
  uploadStorageObject,
  storageObjectURL,
  type StorageObjectInfo,
  type StorageSettingsView,
} from '@/features/storage/api'

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
  const [storage, setStorage] = useState<StorageSettingsView | null>(null)
  const [objects, setObjects] = useState<StorageObjectInfo[]>([])
  const [prefix, setPrefix] = useState('')
  const [uploadKey, setUploadKey] = useState('')
  const [uploadFile, setUploadFile] = useState<File | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [busyKey, setBusyKey] = useState<string | null>(null)
  const [form, setForm] = useState({ disabled: false, url: '', token: '' })
  const fileInputRef = useRef<HTMLInputElement>(null)

  const enabled = storage?.effective.status === 'enabled'
  const status   = storage?.effective.status ?? 'disabled'
  const backend  = storage?.effective.backend || '—'
  const restartRequired = storage?.restart_required ?? false

  async function loadStorage() {
    const st = await getStorageSettings().catch(() => null)
    setStorage(st)
    if (st) {
      if (st.config) setForm(st.config)
      if (st.effective.status === 'enabled') {
        const objs = await listStorageObjects(projectId, prefix).catch(() => [])
        setObjects(objs)
      } else {
        setObjects([])
      }
    }
    setLoading(false)
  }

  useEffect(() => { void loadStorage() }, []) // eslint-disable-line react-hooks/exhaustive-deps

  async function handleRefresh() {
    setRefreshing(true)
    try {
      setObjects(await listStorageObjects(projectId, prefix))
    } catch {
      setObjects([])
    } finally {
      setRefreshing(false)
    }
  }

  async function handleSave() {
    setSaving(true)
    try {
      const next = await saveStorageSettings({ disabled: form.disabled, url: form.url.trim(), token: form.token })
      setStorage(next)
      setForm(next.config)
    } finally {
      setSaving(false)
    }
  }

  async function handleDelete(key: string) {
    if (!confirm(`Delete stored object "${key}"?`)) return
    setBusyKey(key)
    try {
      await deleteStorageObject(projectId, key)
      await handleRefresh()
    } finally {
      setBusyKey(null)
    }
  }

  async function handleUpload() {
    if (!uploadFile) return
    setUploading(true)
    try {
      await uploadStorageObject(projectId, uploadFile, uploadKey.trim() || uploadFile.name)
      await handleRefresh()
      setUploadFile(null)
      setUploadKey('')
      if (fileInputRef.current) fileInputRef.current.value = ''
    } finally {
      setUploading(false)
    }
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
            disabled={busyKey === row.original.key}
            onClick={e => { e.stopPropagation(); void handleDelete(row.original.key) }}
            className="text-destructive hover:bg-destructive/10"
          />
        </div>
      ),
    },
  ]

  if (loading) {
    return (
      <DataBodyTemplate title="Storage">
        <DataBodyTemplate.Body>
          <p className="text-sm text-muted-foreground">Loading…</p>
        </DataBodyTemplate.Body>
      </DataBodyTemplate>
    )
  }

  return (
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
        <Button size="sm" onClick={() => void handleSave()} disabled={saving || !storage}>
          <Save className="mr-2 size-4" />
          {saving ? 'Saving…' : 'Save'}
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
            <input
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
              disabled={!enabled || !uploadFile || uploading}
            >
              <Save className="mr-2 size-4" />
              {uploading ? 'Uploading…' : 'Upload'}
            </Button>
          </DataBodyTemplate.Field>
        </DataBodyTemplate.Group>

        <DataBodyTemplate.Group
          layout="stacked"
          title="Stored Objects"
          description="Browse and download stored artifacts from the active object store."
          actions={
            <div className="flex items-center gap-2">
              <Input
                value={prefix}
                onChange={e => setPrefix(e.target.value)}
                placeholder="prefix filter"
                className="w-52"
                onKeyDown={(e) => {
                  if (e.key === 'Enter') { e.preventDefault(); void handleRefresh() }
                }}
              />
              <Button
                variant="outline"
                size="sm"
                onClick={() => void handleRefresh()}
                disabled={refreshing || !enabled}
              >
                <RefreshCw className={refreshing ? 'size-4 animate-spin' : 'size-4'} />
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
                : 'No stored objects found for this prefix.'
            }
            tableWidthMode="fill-last"
            rowHeight={44}
          />
        </DataBodyTemplate.Group>

      </DataBodyTemplate.Body>
    </DataBodyTemplate>
  )
}
