import { ExternalLink, RefreshCw, Square, Trash2, X } from 'lucide-react'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import StatusBadge from '@/shared/components/StatusBadge'
import { YamlMirror } from '@/components/ui/yaml-mirror'
import { useNotebook, useStopNotebook, useStartNotebook, useDeleteNotebook } from '@/features/notebooks/hooks'
import { Link } from '@/lib/router'
import { fmtDate } from '@/lib/format'
import { useDeleteTarget } from '@/shared/hooks/useDeleteTarget'
import { ConfirmDialog } from '@/shared/components/ConfirmDialog'

export function NotebookDetailPanel({ name, projectId }: { name: string; projectId: string }) {
  const { close } = useSidePanel()
  const { data: notebook, isLoading } = useNotebook(name)
  const { mutateAsync: stop, isPending: stopping } = useStopNotebook()
  const { mutateAsync: start, isPending: starting } = useStartNotebook()
  const { mutateAsync: del, isPending: deleting } = useDeleteNotebook()
  const deleteTarget = useDeleteTarget<string>()

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
    <>
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
            onClick={() => deleteTarget.requestDelete(name)}
            className="text-muted-foreground hover:text-destructive" />
          {closeBtn}
        </div>
      }
    >
      <PanelTemplate.Section title="Details">
        <dl className="space-y-2">
          <PanelTemplate.Row label="Environment">{notebook.env || notebook.image || '—'}</PanelTemplate.Row>
          <PanelTemplate.Row label="Work Dir">{notebook.work_dir || '—'}</PanelTemplate.Row>
          <PanelTemplate.Row label="Volume">{notebook.volume_id || '—'}</PanelTemplate.Row>
          <PanelTemplate.Row label="Runtime">{notebook.runtime_id || '—'}</PanelTemplate.Row>
          <PanelTemplate.Row label="Endpoint">{notebook.endpoint || '—'}</PanelTemplate.Row>
          <PanelTemplate.Row label="Created">{fmtDate(notebook.created_at)}</PanelTemplate.Row>
        </dl>
      </PanelTemplate.Section>

      <PanelTemplate.Section title="Execution">
        <Link
          to={`/projects/${projectId}/notebook-executions?notebook=${encodeURIComponent(notebook.name)}`}
          className="text-sm text-primary hover:underline"
        >
          View execution history and approvals
        </Link>
      </PanelTemplate.Section>

      <PanelTemplate.Section title="Notebook YAML">
        <YamlMirror value={notebook.yaml || ''} readOnly className="min-h-[14rem]" />
      </PanelTemplate.Section>
    </PanelTemplate>

    <ConfirmDialog
      open={deleteTarget.open}
      onCancel={deleteTarget.cancel}
      title="Delete this notebook?"
      description={<>"{deleteTarget.target}" will be deleted. The volume and work directory are preserved.</>}
      error={deleteTarget.error}
      confirmLabel={deleting ? 'Deleting…' : 'Delete notebook'}
      pending={deleting}
      onConfirm={() => void deleteTarget.confirm(async target => { await del(target); void close() })}
    />
    </>
  )
}
