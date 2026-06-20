import { useEffect, useMemo, useState } from 'react'
import { useProjectId } from '@/lib/projectContext'
import { Download, RefreshCw, Save, Trash2 } from 'lucide-react'
import { DataBodyTemplate } from '@loykin/designkit'
import { Badge } from '@/components/ui/badge'
import { Button, buttonVariants } from '@/components/ui/button'
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
    case 'enabled':
      return 'default'
    case 'unavailable':
      return 'destructive'
    default:
      return 'secondary'
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

  const enabled = storage?.effective.status === 'enabled'
  const backend = storage?.effective.backend || '—'

  async function loadStorage() {
    const st = await getStorageSettings().catch(() => null)
    setStorage(st)
    if (st) {
      if (st.config) {
        setForm(st.config)
      }
      if (st.effective.status === 'enabled') {
        const objs = await listStorageObjects(projectId, prefix).catch(() => [])
        setObjects(objs)
      } else {
        setObjects([])
      }
    }
    setLoading(false)
  }

  const [form, setForm] = useState({
    disabled: false,
    url: '',
    token: '',
  })

  useEffect(() => {
    void loadStorage()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const restartRequired = storage?.restart_required ?? false
  const status = storage?.effective.status ?? 'disabled'

  async function handleRefresh() {
    setRefreshing(true)
    try {
      const objs = await listStorageObjects(projectId, prefix)
      setObjects(objs)
    } catch {
      setObjects([])
    } finally {
      setRefreshing(false)
    }
  }

  async function handleSave() {
    setSaving(true)
    try {
      const next = await saveStorageSettings({
        disabled: form.disabled,
        url: form.url.trim(),
        token: form.token,
      })
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
      const key = uploadKey.trim() || uploadFile.name
      await uploadStorageObject(projectId, uploadFile, key)
      await handleRefresh()
      setUploadFile(null)
      setUploadKey('')
    } finally {
      setUploading(false)
    }
  }

  const objectRows = useMemo(() => objects, [objects])

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
      actions={
        <>
          <Badge variant={statusVariant(status)}>{status}</Badge>
          {restartRequired && <Badge variant="outline">Restart required</Badge>}
          <Button variant="outline" size="sm" onClick={() => void handleRefresh()} disabled={refreshing || !storage || !enabled}>
            <RefreshCw className={`mr-2 size-4 ${refreshing ? 'animate-spin' : ''}`} />
            Refresh
          </Button>
          <Button size="sm" onClick={() => void handleSave()} disabled={saving || !storage}>
            <Save className="mr-2 size-4" />
            {saving ? 'Saving…' : 'Save'}
          </Button>
        </>
      }
    >
      <DataBodyTemplate.Body>
        <DataBodyTemplate.Group variant="bordered" title="Artifact Store Config">
          <div className="space-y-4 text-sm">
            <div className="grid gap-4 sm:grid-cols-2">
              <div>
                <p className="text-xs text-muted-foreground">Enabled</p>
                <div className="mt-2 flex items-center gap-3">
                  <Switch
                    checked={!form.disabled}
                    onCheckedChange={(checked) => setForm(prev => ({ ...prev, disabled: !checked }))}
                  />
                  <span className="text-sm">
                    {!form.disabled ? 'Enabled' : 'Disabled'}
                  </span>
                </div>
                <p className="mt-2 text-xs text-muted-foreground">
                  Disabled hides object store exports and downloads after restart.
                </p>
              </div>

              <div>
                <p className="text-xs text-muted-foreground">Config file</p>
                <p className="mt-2 font-mono text-xs text-foreground break-all">{storage?.config_path || '—'}</p>
                <p className="mt-2 text-xs text-muted-foreground">
                  Saved here. Apply requires a server restart.
                </p>
              </div>
            </div>

            <div className="grid gap-4 sm:grid-cols-2">
              <label className="space-y-2">
                <span className="text-xs text-muted-foreground">storage.url</span>
                <Input
                  value={form.url}
                  onChange={e => setForm(prev => ({ ...prev, url: e.target.value }))}
                  placeholder="s3://bucket?endpoint=http://localhost:9000..."
                />
                <p className="text-xs text-muted-foreground">
                  Leave empty to use the server default artifact store.
                </p>
              </label>

              <label className="space-y-2">
                <span className="text-xs text-muted-foreground">storage.token</span>
                <Input
                  value={form.token}
                  onChange={e => setForm(prev => ({ ...prev, token: e.target.value }))}
                  placeholder="Bearer token for HTTP stores"
                />
              </label>
            </div>

            <div className="rounded-md border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
              <p><span className="font-medium text-foreground">Runtime status:</span> {status}</p>
              <p className="mt-1"><span className="font-medium text-foreground">Backend:</span> {backend}</p>
              <p className="mt-1"><span className="font-medium text-foreground">Reason:</span> {storage?.effective.reason || '—'}</p>
              <p className="mt-1">Changing these values writes the persisted storage config. Restart the server to apply it to the running store.</p>
            </div>
          </div>
        </DataBodyTemplate.Group>

        <DataBodyTemplate.Group variant="bordered" title="Upload Object">
          <div className="space-y-3">
            <div className="grid gap-4 sm:grid-cols-2">
              <label className="space-y-2">
                <span className="text-xs text-muted-foreground">Object key</span>
                <Input
                  value={uploadKey}
                  onChange={e => setUploadKey(e.target.value)}
                  placeholder="runs/run-123/model/model.bin"
                  disabled={!enabled}
                />
                <p className="text-xs text-muted-foreground">
                  Leave empty to use the selected file name.
                </p>
              </label>
              <label className="space-y-2">
                <span className="text-xs text-muted-foreground">File</span>
                <input
                  type="file"
                  onChange={e => setUploadFile(e.target.files?.[0] ?? null)}
                  disabled={!enabled}
                  className="block w-full cursor-pointer rounded-lg border border-input bg-transparent px-2.5 py-1.5 text-sm text-foreground file:mr-3 file:rounded-md file:border-0 file:bg-muted file:px-3 file:py-1 file:text-sm file:font-medium file:text-foreground hover:file:bg-muted/80 disabled:cursor-not-allowed disabled:opacity-50"
                />
              </label>
            </div>
            <div className="flex items-center gap-2">
              <Button
                size="sm"
                onClick={() => void handleUpload()}
                disabled={!enabled || !uploadFile || uploading}
              >
                <Save className="mr-2 size-4" />
                {uploading ? 'Uploading…' : 'Upload'}
              </Button>
              <p className="text-xs text-muted-foreground">
                Uploads directly into the active store. Object store must be enabled.
              </p>
            </div>
          </div>
        </DataBodyTemplate.Group>

        <DataBodyTemplate.Group
          variant="bordered"
          title="Stored Objects"
          description="Browse and download stored artifacts from the active object store."
          actions={
            <Input
              value={prefix}
              onChange={e => setPrefix(e.target.value)}
              placeholder="prefix filter"
              className="w-52"
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault()
                  void handleRefresh()
                }
              }}
            />
          }
        >
          <div>
            {!enabled ? (
              <div className="rounded-md border bg-muted/20 px-3 py-4 text-sm text-muted-foreground">
                Object storage is disabled or unavailable. Save and restart the server to browse stored objects.
              </div>
            ) : objectRows.length === 0 ? (
              <div className="rounded-md border bg-muted/20 px-3 py-4 text-sm text-muted-foreground">
                No stored objects found for this prefix.
              </div>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full min-w-[720px] text-left text-sm">
                  <thead className="border-b text-xs uppercase tracking-wide text-muted-foreground">
                    <tr>
                      <th className="py-2 pr-4 font-medium">Key</th>
                      <th className="py-2 pr-4 font-medium">Size</th>
                      <th className="py-2 pr-4 font-medium">Modified</th>
                      <th className="py-2 pr-4 font-medium text-right">Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {objectRows.map((obj) => (
                      <tr key={obj.key} className="border-b last:border-0">
                        <td className="py-3 pr-4 font-mono text-xs break-all">{obj.key}</td>
                        <td className="py-3 pr-4 text-xs text-muted-foreground">{fmtBytes(obj.size)}</td>
                        <td className="py-3 pr-4 text-xs text-muted-foreground">{fmtDate(obj.modified_at)}</td>
                        <td className="py-3 pr-0">
                          <div className="flex justify-end gap-2">
                            <a
                              href={storageObjectURL(projectId, obj.key)}
                              target="_blank"
                              rel="noreferrer"
                              className={buttonVariants({ variant: 'outline', size: 'sm' })}
                            >
                              <Download className="mr-2 size-4" />
                              Download
                            </a>
                            <Button
                              variant="destructive"
                              size="sm"
                              onClick={() => void handleDelete(obj.key)}
                              disabled={busyKey === obj.key}
                            >
                              <Trash2 className="mr-2 size-4" />
                              Delete
                            </Button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </DataBodyTemplate.Group>
      </DataBodyTemplate.Body>
    </DataBodyTemplate>
  )
}
