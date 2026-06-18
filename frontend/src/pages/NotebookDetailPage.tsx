import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { ExternalLink, RefreshCw, Square, Trash2 } from 'lucide-react'
import { DetailBodyTemplate } from '@loykin/designkit'
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
import { useProjectId } from '@/lib/projectContext'
import { YamlMirror } from '@/components/ui/yaml-mirror'

export default function NotebookDetailPage() {
  const { name } = useParams<{ name: string }>()
  const navigate = useNavigate()
  const projectId = useProjectId()
  const [notebook, setNotebook] = useState<NotebookServer | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)

  async function load() {
    if (!name || !projectId) return
    const nb = await getNotebook(projectId, name).catch(() => null)
    setNotebook(nb)
    setLoading(false)
  }

  useEffect(() => {
    void load()
    const t = setInterval(() => void load(), 5000)
    return () => clearInterval(t)
  }, [name])

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
      <DetailBodyTemplate title="Loading…">
        <DetailBodyTemplate.Section>
          <p className="text-sm text-muted-foreground">Loading…</p>
        </DetailBodyTemplate.Section>
      </DetailBodyTemplate>
    )
  }

  if (!notebook) {
    return (
      <DetailBodyTemplate title="Not Found">
        <DetailBodyTemplate.Section>
          <p className="text-sm text-muted-foreground">Notebook not found.</p>
        </DetailBodyTemplate.Section>
      </DetailBodyTemplate>
    )
  }

  async function handleStop() {
    if (!name) return
    await withBusy(() => stopNotebook(projectId, name))
  }

  async function handleStart() {
    if (!name) return
    await withBusy(() => startNotebook(projectId, name).then(() => {}))
  }

  async function handleDelete() {
    if (!name || !confirm(`Delete notebook "${name}"?\nThe volume and work directory are preserved.`)) return
    await withBusy(() => deleteNotebook(projectId, name).then(() => navigate(`/projects/${projectId}/notebooks`)))
  }

  return (
    <DetailBodyTemplate
      eyebrow={<Link to={`/projects/${projectId}/notebooks`} className="hover:text-foreground transition-colors">← Notebooks</Link>}
      title={notebook.name}
      description="Notebook server detail and workspace status"
      status={<StatusBadge status={notebook.status} />}
      actions={
        <div className="flex items-center gap-1">
          {notebook.status === 'running' && (
            <a
              href={notebookProxyURL(projectId, notebook.name)}
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
        </div>
      }
    >
      <DetailBodyTemplate.Section title="Details" surface="bordered">
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
      </DetailBodyTemplate.Section>

      <DetailBodyTemplate.Section title="Notebook YAML" surface="bordered">
        <YamlMirror value={notebook.yaml || ''} readOnly className="min-h-[16rem]" />
      </DetailBodyTemplate.Section>
    </DetailBodyTemplate>
  )
}
