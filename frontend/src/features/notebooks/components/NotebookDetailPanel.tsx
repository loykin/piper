import { ExternalLink, RefreshCw, Square, Trash2 } from 'lucide-react'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { IconButton } from '@/components/ui/icon-button'
import StatusBadge from '@/shared/components/StatusBadge'
import { YamlMirror } from '@/components/ui/yaml-mirror'
import { useNotebook, useStopNotebook, useStartNotebook } from '@/features/notebooks/hooks'
import { Link } from '@/lib/router'
import { fmtDate } from '@/lib/format'
import { useDeleteTarget } from '@/shared/hooks/useDeleteTarget'
import { PanelCloseButton, PanelPlaceholder } from '@/shared/components/PanelPlaceholder'
import { DeleteNotebookDialog } from './DeleteNotebookDialog'
import { MutationErrors } from '@/shared/components/MutationErrors'
import { hasMutationError } from '@/shared/mutationErrors'

export function NotebookDetailPanel({ name, projectId }: { name: string; projectId: string }) {
  const { close } = useSidePanel()
  const query = useNotebook(name)
  const notebook = query.data
  const stopMutation = useStopNotebook()
  const startMutation = useStartNotebook()
  const stopping = stopMutation.isPending
  const starting = startMutation.isPending
  const deleteTarget = useDeleteTarget<string>()

  const busy = stopping || starting


  if (!notebook) return <PanelPlaceholder query={query} noun="notebook" />

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
              onClick={() => stopMutation.mutate(name)}
              className="text-destructive hover:bg-destructive/10" />
          )}
          {(notebook.status === 'stopped' || notebook.status === 'failed') && (
            <IconButton icon={<RefreshCw />} label="Start" disabled={busy}
              onClick={() => startMutation.mutate(name)} />
          )}
          <IconButton icon={<Trash2 />} label="Delete" disabled={busy}
            onClick={() => deleteTarget.requestDelete(name)}
            className="text-muted-foreground hover:text-destructive" />
          <PanelCloseButton />
        </div>
      }
    >
      {hasMutationError([stopMutation, startMutation]) && (
        <PanelTemplate.Section>
          <MutationErrors of={[stopMutation, startMutation]} />
        </PanelTemplate.Section>
      )}
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

    <DeleteNotebookDialog target={deleteTarget} onDeleted={() => void close()} />
    </>
  )
}
