import { useNavigate } from '@/lib/router'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { ExternalLink, FlaskConical, Pencil, Trash2, X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import StatusBadge from '@/shared/components/StatusBadge'
import { useProjectId } from '@/features/projects/context'
import { useDeleteMLflowIntegration, useMLflowIntegration, useTestMLflowIntegration } from '../hooks'
import { toneText, warningNotice } from '@/shared/status'
import { useDeleteTarget } from '@/shared/hooks/useDeleteTarget'
import { ConfirmDialog } from '@/shared/components/ConfirmDialog'

export function MLflowIntegrationDetailPanel({ id, canAdmin }: { id: string; canAdmin: boolean }) {
  const { close } = useSidePanel(); const navigate = useNavigate(); const projectId = useProjectId()
  const query = useMLflowIntegration(id); const test = useTestMLflowIntegration(); const remove = useDeleteMLflowIntegration(); const deleteTarget = useDeleteTarget<string>()
  const item = query.data
  if (!item) return <PanelTemplate title={query.isLoading ? 'Loading…' : 'Integration not found'} actions={<Button variant="ghost" size="icon-sm" onClick={() => void close()}><X /></Button>} />
  return <><PanelTemplate eyebrow="MLflow integration" title={item.name} status={<StatusBadge status={item.health} />} actions={<div className="flex gap-1">{canAdmin && <Button variant="ghost" size="icon-sm" onClick={() => void navigate(`/projects/${projectId}/integrations/mlflow/${id}/edit`)}><Pencil /><span className="sr-only">Edit</span></Button>}{canAdmin && <Button variant="ghost" size="icon-sm" onClick={() => deleteTarget.requestDelete(id)} className="text-destructive"><Trash2 /><span className="sr-only">Delete</span></Button>}<Button variant="ghost" size="icon-sm" onClick={() => void close()}><X /><span className="sr-only">Close</span></Button></div>}>
    <PanelTemplate.Section title="Connection"><dl className="space-y-2"><PanelTemplate.Row label="Tracking host"><a href={item.tracking_uri} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-primary hover:underline">{item.tracking_uri}<ExternalLink className="size-3" /></a></PanelTemplate.Row><PanelTemplate.Row label="Credential">{item.credential_ref || '—'}</PanelTemplate.Row><PanelTemplate.Row label="Default">{item.default ? 'Yes' : 'No'}</PanelTemplate.Row><PanelTemplate.Row label="Pipeline export">{item.export_pipelines ? 'Enabled' : 'Disabled'}</PanelTemplate.Row><PanelTemplate.Row label="Notebook export">{item.export_notebook_executions ? 'Enabled' : 'Disabled'}</PanelTemplate.Row></dl></PanelTemplate.Section>
    <PanelTemplate.Section title="Health">{!item.system_enabled && <p className={`mb-3 p-3 text-xs ${warningNotice}`}>MLflow dispatch is disabled in the server configuration. Connection settings are preserved, but events will not be exported until an operator enables integrations.mlflow.enabled.</p>}<dl className="space-y-2"><PanelTemplate.Row label="Pending events">{item.pending_events}</PanelTemplate.Row><PanelTemplate.Row label="Dead events">{item.dead_events}</PanelTemplate.Row><PanelTemplate.Row label="Oldest pending">{item.oldest_pending_age_seconds ? `${Math.round(item.oldest_pending_age_seconds)}s` : '—'}</PanelTemplate.Row></dl>{canAdmin && <Button className="mt-3" size="sm" variant="outline" disabled={test.isPending} onClick={() => test.mutate(id)}><FlaskConical />{test.isPending ? 'Testing…' : 'Test Connection'}</Button>}{test.data && <p className={`mt-2 text-xs ${test.data.ok ? toneText.success : 'text-destructive'}`}>{test.data.message}</p>}{test.error && <p className="mt-2 text-xs text-destructive">{test.error.message}</p>}</PanelTemplate.Section>
  </PanelTemplate><ConfirmDialog
    open={deleteTarget.open}
    onCancel={deleteTarget.cancel}
    title="Delete this integration?"
    description="Export stops and pending events are preserved as disabled. Existing MLflow runs are not deleted."
    error={deleteTarget.error}
    pending={remove.isPending}
    confirmLabel="Delete"
    onConfirm={() => void deleteTarget.confirm(async target => { await remove.mutateAsync(target); void close() })}
  /></>
}
