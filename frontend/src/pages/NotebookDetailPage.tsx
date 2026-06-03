import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { ExternalLink, RefreshCw, Square, Trash2 } from 'lucide-react'
import { DataPage } from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import StatusBadge from '@/shared/components/StatusBadge'
import {
  getNotebook,
  notebookProxyURL,
  startNotebook,
  stopNotebook,
  deleteNotebook,
  type NotebookServer,
} from '@/features/notebooks/api'
import { buildNotebookPromotionDraft } from '@/features/notebooks/promotion'
import { YamlMirror } from '@/components/ui/yaml-mirror'

export default function NotebookDetailPage() {
  const { name } = useParams<{ name: string }>()
  const navigate = useNavigate()
  const [notebook, setNotebook] = useState<NotebookServer | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)

  async function load() {
    if (!name) return
    const nb = await getNotebook(name).catch(() => null)
    setNotebook(nb)
    setLoading(false)
  }

  useEffect(() => {
    void load()
    const t = setInterval(() => void load(), 5000)
    return () => clearInterval(t)
  }, [name])

  const draft = useMemo(() => (notebook ? buildNotebookPromotionDraft(notebook) : ''), [notebook])

  async function withBusy(fn: () => Promise<void>) {
    setBusy(true)
    try {
      await fn()
      await load()
    } finally {
      setBusy(false)
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

  async function handleStop() {
    if (!name) return
    await withBusy(() => stopNotebook(name))
  }

  async function handleStart() {
    if (!name) return
    await withBusy(() => startNotebook(name).then(() => {}))
  }

  async function handleDelete() {
    if (!name || !confirm(`Delete notebook "${name}"?\nThe volume and work directory are preserved.`)) return
    await withBusy(() => deleteNotebook(name).then(() => navigate('/notebooks')))
  }

  return (
    <DataPage>
      <DataPage.Header>
        <DataPage.TitleBlock
          breadcrumb={<Link to="/notebooks" className="hover:text-foreground transition-colors">← Notebooks</Link>}
          title={notebook.name}
          description="Notebook server detail and promotion snapshot"
        />
        <DataPage.Actions>
          <StatusBadge status={notebook.status} />
          <Button variant="outline" size="sm" onClick={() => navigate(`/notebooks/${notebook.name}/promote`)}>
            Promote to Pipeline
          </Button>
          {notebook.status === 'running' && (
            <a
              href={notebookProxyURL(notebook.name)}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1 rounded-md border px-3 py-2 text-sm font-medium hover:bg-muted"
            >
              <ExternalLink size={16} />
              Open
            </a>
          )}
          {notebook.status === 'running' && (
            <IconButton icon={<Square />} label="Stop" disabled={busy} onClick={handleStop} className="text-destructive hover:bg-destructive/10" />
          )}
          {(notebook.status === 'stopped' || notebook.status === 'failed') && (
            <IconButton icon={<RefreshCw />} label="Start" disabled={busy} onClick={handleStart} />
          )}
          <IconButton icon={<Trash2 />} label="Delete" disabled={busy} onClick={handleDelete} className="text-muted-foreground hover:text-destructive" />
        </DataPage.Actions>
      </DataPage.Header>

      <DataPage.Content>
        <DataPage.Group surface="bordered" className="mb-4">
          <div className="p-4">
            <dl className="grid grid-cols-2 gap-4 sm:grid-cols-3">
              <div><dt className="text-xs text-muted-foreground">Status</dt><dd className="mt-1"><StatusBadge status={notebook.status} /></dd></div>
              <div><dt className="text-xs text-muted-foreground">Worker</dt><dd className="mt-1 text-sm">{notebook.worker_id || '—'}</dd></div>
              <div><dt className="text-xs text-muted-foreground">Endpoint</dt><dd className="mt-1 font-mono text-xs">{notebook.endpoint || '—'}</dd></div>
              <div><dt className="text-xs text-muted-foreground">Env</dt><dd className="mt-1 font-mono text-xs">{notebook.env || '—'}</dd></div>
              <div><dt className="text-xs text-muted-foreground">Image</dt><dd className="mt-1 font-mono text-xs">{notebook.image || '—'}</dd></div>
              <div><dt className="text-xs text-muted-foreground">Work Dir</dt><dd className="mt-1 font-mono text-xs">{notebook.work_dir || '—'}</dd></div>
              <div><dt className="text-xs text-muted-foreground">Volume</dt><dd className="mt-1 font-mono text-xs">{notebook.volume_id || '—'}</dd></div>
              <div><dt className="text-xs text-muted-foreground">Created</dt><dd className="mt-1 text-sm">{new Date(notebook.created_at).toLocaleString()}</dd></div>
              <div><dt className="text-xs text-muted-foreground">Updated</dt><dd className="mt-1 text-sm">{new Date(notebook.updated_at).toLocaleString()}</dd></div>
            </dl>
          </div>
        </DataPage.Group>

        <DataPage.Group surface="bordered" className="mb-4">
          <DataPage.GroupHeader title="Notebook YAML" className="px-4 pt-3" />
          <YamlMirror value={notebook.yaml || ''} readOnly className="min-h-[16rem]" />
        </DataPage.Group>

        <DataPage.Group surface="bordered">
          <DataPage.GroupHeader title="Promotion Snapshot" className="px-4 pt-3" />
          <pre className="overflow-x-auto px-4 pb-4 text-xs leading-6 text-muted-foreground">{draft}</pre>
        </DataPage.Group>
      </DataPage.Content>
    </DataPage>
  )
}
