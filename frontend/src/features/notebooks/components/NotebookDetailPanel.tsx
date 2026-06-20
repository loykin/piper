import { ExternalLink, RefreshCw, Square, Trash2, X } from 'lucide-react'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import StatusBadge from '@/shared/components/StatusBadge'
import { YamlMirror } from '@/components/ui/yaml-mirror'
import { useNotebook, useStopNotebook, useStartNotebook, useDeleteNotebook } from '@/features/notebooks/hooks'

export function NotebookDetailPanel({ name, projectId }: { name: string; projectId: string }) {
  const { close } = useSidePanel()
  const { data: notebook, isLoading } = useNotebook(name)
  const { mutateAsync: stop, isPending: stopping } = useStopNotebook()
  const { mutateAsync: start, isPending: starting } = useStartNotebook()
  const { mutateAsync: del } = useDeleteNotebook()

  const busy = stopping || starting

  const closeBtn = (
    <Button variant="ghost" size="icon-sm" onClick={() => void close()}>
      <X className="h-3.5 w-3.5" />
    </Button>
  )

  if (isLoading) {
    return (
      <PanelTemplate title="Loading…" actions={closeBtn}>
        <PanelTemplate.Section>
          <p className="text-xs text-muted-foreground">Loading…</p>
        </PanelTemplate.Section>
      </PanelTemplate>
    )
  }

  if (!notebook) {
    return (
      <PanelTemplate title="Not Found" actions={closeBtn}>
        <PanelTemplate.Section>
          <p className="text-xs text-muted-foreground">Notebook not found.</p>
        </PanelTemplate.Section>
      </PanelTemplate>
    )
  }

  const proxyURL = `/api/projects/${projectId}/notebooks/${notebook.name}/proxy/`

  return (
    <PanelTemplate
      eyebrow="Notebook Server"
      title={notebook.name}
      status={<StatusBadge status={notebook.status} />}
      actions={
        <div className="flex items-center gap-1">
          {notebook.status === 'running' && (
            <a href={proxyURL} target="_blank" rel="noreferrer"
              className="inline-flex h-7 w-7 items-center justify-center rounded-[min(var(--radius-md),12px)] text-primary hover:bg-muted">
              <ExternalLink size={14} />
            </a>
          )}
          {notebook.status === 'running' && (
            <IconButton icon={<Square />} label="Stop" disabled={busy}
              onClick={() => void stop(name)}
              className="text-destructive hover:bg-destructive/10" />
          )}
          {(notebook.status === 'stopped' || notebook.status === 'failed') && (
            <IconButton icon={<RefreshCw />} label="Start" disabled={busy}
              onClick={() => void start(name)} />
          )}
          <IconButton icon={<Trash2 />} label="Delete" disabled={busy}
            onClick={() => {
              if (!confirm(`Delete notebook "${name}"?\nThe volume and work directory are preserved.`)) return
              void del(name).then(() => void close())
            }}
            className="text-muted-foreground hover:text-destructive" />
          {closeBtn}
        </div>
      }
    >
      <PanelTemplate.Section title="Details">
        <dl className="grid grid-cols-2 gap-3">
          <div>
            <dt className="text-xs text-muted-foreground">Environment</dt>
            <dd className="mt-0.5 break-all font-mono text-xs">{notebook.env || notebook.image || '—'}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Work Dir</dt>
            <dd className="mt-0.5 break-all font-mono text-xs">{notebook.work_dir || '—'}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Volume</dt>
            <dd className="mt-0.5 font-mono text-xs">{notebook.volume_id || '—'}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Worker</dt>
            <dd className="mt-0.5 text-xs">{notebook.worker_id || '—'}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Endpoint</dt>
            <dd className="mt-0.5 break-all font-mono text-xs">{notebook.endpoint || '—'}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Created</dt>
            <dd className="mt-0.5 text-xs">{new Date(notebook.created_at).toLocaleString()}</dd>
          </div>
        </dl>
      </PanelTemplate.Section>

      <PanelTemplate.Section title="Notebook YAML">
        <YamlMirror value={notebook.yaml || ''} readOnly className="min-h-[14rem]" />
      </PanelTemplate.Section>
    </PanelTemplate>
  )
}
