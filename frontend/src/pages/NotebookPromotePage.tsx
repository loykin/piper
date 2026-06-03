import { useEffect, useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { Download, Copy, Upload } from 'lucide-react'
import {
  DataPage,
  Select, SelectTrigger, SelectContent, SelectItem, SelectValue,
} from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { YamlMirror } from '@/components/ui/yaml-mirror'
import StatusBadge from '@/shared/components/StatusBadge'
import {
  exportNotebookPromotion,
  getNotebook,
  getNotebookPromotion,
  listNotebookPromotionExports,
  type NotebookPromotionPreview,
  type NotebookPromotionExportRecord,
  type NotebookPromotionTarget,
  type NotebookServer,
} from '@/features/notebooks/api'
import { getSystemSettings, type SystemSettings } from '@/features/system/api'
import { buildNotebookPromotionDraft } from '@/features/notebooks/promotion'

function downloadText(name: string, text: string) {
  const blob = new Blob([text], { type: 'text/yaml;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${name}-promotion.yaml`
  a.click()
  URL.revokeObjectURL(url)
}

export default function NotebookPromotePage() {
  const { name } = useParams<{ name: string }>()
  const [notebook, setNotebook] = useState<NotebookServer | null>(null)
  const [preview, setPreview] = useState<NotebookPromotionPreview | null>(null)
  const [exports, setExports] = useState<NotebookPromotionExportRecord[]>([])
  const [settings, setSettings] = useState<SystemSettings | null>(null)
  const [loading, setLoading] = useState(true)
  const [exporting, setExporting] = useState(false)
  const [copied, setCopied] = useState(false)
  const [target, setTarget] = useState<NotebookPromotionTarget>('draft')
  const [message, setMessage] = useState<string | null>(null)

  async function load() {
    if (!name) return
    const [nb, sys] = await Promise.all([
      getNotebook(name).catch(() => null),
      getSystemSettings().catch(() => null),
    ])
    setNotebook(nb)
    setSettings(sys)
    setLoading(false)
  }

  useEffect(() => {
    void load()
  }, [name])

  useEffect(() => {
    if (!name) return
    setMessage(null)
    void getNotebookPromotion(name, target).then(setPreview).catch(() => setPreview(null))
    void listNotebookPromotionExports(name).then(setExports).catch(() => setExports([]))
  }, [name, target])

  const draft = useMemo(() => {
    if (preview?.draft) return preview.draft
    return notebook ? buildNotebookPromotionDraft(notebook, target) : ''
  }, [notebook, preview, target])
  const artifactStoreEnabled = settings?.artifact_store.status === 'enabled'

  useEffect(() => {
    if (!artifactStoreEnabled && target === 'object_store') {
      setTarget('draft')
    }
  }, [artifactStoreEnabled, target])

  async function copyDraft() {
    await navigator.clipboard.writeText(draft)
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1200)
  }

  async function handleExport() {
    if (!name) return
    setExporting(true)
    setMessage(null)
    try {
      const result = await exportNotebookPromotion(name, target)
      setPreview(result)
      void listNotebookPromotionExports(name).then(setExports).catch(() => setExports([]))
      setMessage(result.object_key
        ? `Exported to object store key ${result.object_key}`
        : result.bundle_path
          ? `Exported to ${result.bundle_path}`
          : 'Export completed')
      if (target === 'download' && result.bundle_path) {
        const fileName = result.bundle_path.split('/').pop() || `${name}-promotion.yaml`
        window.location.assign(`/api/notebooks/${encodeURIComponent(name)}/promotion/exports/${encodeURIComponent(fileName)}`)
      }
    } catch (err) {
      setMessage(err instanceof Error ? err.message : 'export failed')
    } finally {
      setExporting(false)
    }
  }

  if (loading) {
    return (
      <DataPage>
        <DataPage.Content>
          <p className="text-sm text-muted-foreground">Loading…</p>
        </DataPage.Content>
      </DataPage>
    )
  }

  if (!notebook) {
    return (
      <DataPage>
        <DataPage.Content>
          <p className="text-sm text-muted-foreground">Notebook not found.</p>
        </DataPage.Content>
      </DataPage>
    )
  }

  return (
    <DataPage>
      <DataPage.Header>
        <DataPage.TitleBlock
          breadcrumb={<Link to={`/notebooks/${notebook.name}`} className="hover:text-foreground transition-colors">← Notebook</Link>}
          title={`${notebook.name} · Promote`}
          description="Review the promotion draft before exporting it as a pipeline definition."
        />
        <DataPage.Actions>
          <StatusBadge status={notebook.status} />
          <Select value={target} onValueChange={v => setTarget((v ?? 'draft') as NotebookPromotionTarget)}>
            <SelectTrigger size="sm" className="w-[180px]">
              <SelectValue placeholder="— export target —" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="draft">Draft only</SelectItem>
              <SelectItem value="download">Download bundle</SelectItem>
              <SelectItem value="repo">Save to repo</SelectItem>
              {artifactStoreEnabled && <SelectItem value="object_store">Upload to object store</SelectItem>}
            </SelectContent>
          </Select>
          <Button variant="outline" size="sm" onClick={copyDraft}>
            <Copy className="mr-2 size-4" />
            {copied ? 'Copied' : 'Copy Draft'}
          </Button>
          <Button variant="outline" size="sm" onClick={() => downloadText(notebook.name, draft)}>
            <Download className="mr-2 size-4" />
            Download Draft
          </Button>
          <Button size="sm" onClick={handleExport} disabled={exporting}>
            <Upload className="mr-2 size-4" />
            {exporting ? 'Exporting' : 'Export'}
          </Button>
        </DataPage.Actions>
      </DataPage.Header>

      <DataPage.Content>
        <DataPage.Group surface="bordered" className="mb-4">
          <div className="p-4">
            <dl className="grid grid-cols-2 gap-4 sm:grid-cols-3">
              <div><dt className="text-xs text-muted-foreground">Source Run</dt><dd className="mt-1 text-sm">{notebook.name}</dd></div>
              <div><dt className="text-xs text-muted-foreground">Worker</dt><dd className="mt-1 text-sm">{notebook.worker_id || '—'}</dd></div>
              <div><dt className="text-xs text-muted-foreground">Backend</dt><dd className="mt-1 text-sm">{preview?.runtime.backend || '—'}</dd></div>
              <div><dt className="text-xs text-muted-foreground">Mode</dt><dd className="mt-1 text-sm">{preview?.runtime.mode || '—'}</dd></div>
              <div><dt className="text-xs text-muted-foreground">Runtime</dt><dd className="mt-1 text-sm">{preview?.runtime.image || notebook.image || notebook.env || '—'}</dd></div>
              <div><dt className="text-xs text-muted-foreground">Work Dir</dt><dd className="mt-1 font-mono text-xs">{notebook.work_dir || '—'}</dd></div>
              <div><dt className="text-xs text-muted-foreground">Volume</dt><dd className="mt-1 font-mono text-xs">{notebook.volume_id || '—'}</dd></div>
              <div><dt className="text-xs text-muted-foreground">Status</dt><dd className="mt-1"><StatusBadge status={notebook.status} /></dd></div>
            </dl>
          </div>
        </DataPage.Group>

        <DataPage.Group surface="bordered" className="mb-4">
          <DataPage.GroupHeader title="Export Target" className="px-4 pt-3" />
          <div className="px-4 pb-4 text-sm text-muted-foreground space-y-2">
            {target === 'draft' && <p>Draft only. No repository or storage destination is chosen yet.</p>}
            {target === 'download' && <p>Create a local export bundle for manual review or commit.</p>}
            {target === 'repo' && <p>Render a commit-ready pipeline definition for source control.</p>}
            {target === 'object_store' && <p>Render a bundle that can be uploaded to S3/MinIO and referenced by the pipeline.</p>}
            {!artifactStoreEnabled && <p>Object store export is hidden because artifact storage is disabled in server settings.</p>}
            <p>Export remains read-only. It does not install packages or mutate runtime state.</p>
            {preview?.validation?.messages?.length ? (
              <div className="rounded-md border bg-muted/30 p-3 text-xs text-foreground">
                <div className="mb-1 font-medium">Validation: {preview.validation.status}</div>
                <ul className="list-disc space-y-1 pl-4 text-muted-foreground">
                  {preview.validation.messages.map(m => <li key={m}>{m}</li>)}
                </ul>
              </div>
            ) : null}
            {message ? <p className="text-xs text-foreground">{message}</p> : null}
          </div>
        </DataPage.Group>

        <DataPage.Group surface="bordered" className="mb-4">
          <DataPage.GroupHeader title="Draft Preview" className="px-4 pt-3" />
          <YamlMirror value={draft} readOnly className="min-h-[24rem]" />
        </DataPage.Group>

        <DataPage.Group surface="bordered" className="mb-4">
          <DataPage.GroupHeader title="Recent Exports" className="px-4 pt-3" />
          <div className="px-4 pb-4">
            {exports.length === 0 ? (
              <p className="text-sm text-muted-foreground">No exports yet.</p>
            ) : (
              <div className="space-y-2">
                {exports.map(item => {
                  const fileName = item.bundle_path ? item.bundle_path.split('/').pop() || item.bundle_path : item.manifest_path?.split('/').pop() || item.object_key || item.name
                  return (
                    <div key={`${item.exported_at}-${fileName}`} className="flex items-center justify-between gap-3 rounded-md border px-3 py-2">
                      <div className="min-w-0">
                        <div className="truncate text-sm font-medium">{fileName}</div>
                        <div className="text-xs text-muted-foreground">
                          {item.target} · {new Date(item.exported_at).toLocaleString()}
                        </div>
                      </div>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => window.location.assign(`/api/notebooks/${encodeURIComponent(notebook.name)}/promotion/exports/${encodeURIComponent(fileName)}`)}
                      >
                        <Download className="mr-2 size-4" />
                        Download
                      </Button>
                    </div>
                  )
                })}
              </div>
            )}
          </div>
        </DataPage.Group>

        <DataPage.Group surface="bordered">
          <DataPage.GroupHeader title="Export Rules" className="px-4 pt-3" />
          <div className="px-4 pb-4 text-sm text-muted-foreground space-y-2">
            <p>Export is read-only. It does not install packages or mutate the notebook runtime.</p>
            <p>Resolved params and artifact refs will be added when the export backend is implemented.</p>
            <p>Unsupported steps must fail validation before a draft is saved or downloaded.</p>
          </div>
        </DataPage.Group>
      </DataPage.Content>
    </DataPage>
  )
}
