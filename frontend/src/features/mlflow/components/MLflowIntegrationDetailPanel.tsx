import { useNavigate } from '@/lib/router'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { ExternalLink, FlaskConical, Pencil, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import StatusBadge from '@/shared/components/StatusBadge'
import { useProjectId } from '@/features/projects/context'
import { useDeleteMLflowIntegration, useMLflowIntegration, useTestMLflowIntegration } from '../hooks'
import { toneText, warningNotice } from '@/shared/status'
import { useDeleteTarget } from '@/shared/hooks/useDeleteTarget'
import { ConfirmDialog } from '@/shared/components/ConfirmDialog'
import { PanelCloseButton, PanelPlaceholder } from '@/shared/components/PanelPlaceholder'

export function MLflowIntegrationDetailPanel({ id, canAdmin }: { id: string; canAdmin: boolean }) {
  const { close } = useSidePanel()
  const navigate = useNavigate()
  const projectId = useProjectId()
  const query = useMLflowIntegration(id)
  const test = useTestMLflowIntegration()
  const remove = useDeleteMLflowIntegration()
  const deleteTarget = useDeleteTarget<string>()

  const item = query.data
  if (!item) return <PanelPlaceholder query={query} noun="integration" />

  return (
    <>
      <PanelTemplate
        eyebrow="MLflow Integration"
        title={item.name}
        status={<StatusBadge status={item.health} />}
        actions={
          <div className="flex items-center gap-1">
            {canAdmin && (
              <IconButton
                icon={<Pencil />}
                label="Edit"
                onClick={() => void navigate(`/projects/${projectId}/integrations/mlflow/${id}/edit`)}
              />
            )}
            {canAdmin && (
              <IconButton
                icon={<Trash2 />}
                label="Delete"
                className="text-destructive hover:bg-destructive/10"
                onClick={() => deleteTarget.requestDelete(id)}
              />
            )}
            <PanelCloseButton />
          </div>
        }
      >
        <PanelTemplate.Section title="Connection">
          <dl className="space-y-2">
            <PanelTemplate.Row label="Tracking Host">
              <a href={item.tracking_uri} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-primary hover:underline">
                {item.tracking_uri}
                <ExternalLink className="size-3" />
              </a>
            </PanelTemplate.Row>
            <PanelTemplate.Row label="Credential">{item.credential_ref || '—'}</PanelTemplate.Row>
            <PanelTemplate.Row label="Default">{item.default ? 'Yes' : 'No'}</PanelTemplate.Row>
            <PanelTemplate.Row label="Pipeline Export">{item.export_pipelines ? 'Enabled' : 'Disabled'}</PanelTemplate.Row>
            <PanelTemplate.Row label="Notebook Export">{item.export_notebook_executions ? 'Enabled' : 'Disabled'}</PanelTemplate.Row>
          </dl>
        </PanelTemplate.Section>
        <PanelTemplate.Section title="Health">
          {!item.system_enabled && (
            <p className={`mb-3 p-3 text-xs ${warningNotice}`}>
              MLflow dispatch is disabled in the server configuration. Connection settings are preserved, but events
              will not be exported until an operator enables integrations.mlflow.enabled.
            </p>
          )}
          <dl className="space-y-2">
            <PanelTemplate.Row label="Pending Events">{item.pending_events}</PanelTemplate.Row>
            <PanelTemplate.Row label="Dead Events">{item.dead_events}</PanelTemplate.Row>
            <PanelTemplate.Row label="Oldest Pending">
              {item.oldest_pending_age_seconds ? `${Math.round(item.oldest_pending_age_seconds)}s` : '—'}
            </PanelTemplate.Row>
          </dl>
          {canAdmin && (
            <Button className="mt-3" size="sm" variant="outline" disabled={test.isPending} onClick={() => test.mutate(id)}>
              <FlaskConical />
              {test.isPending ? 'Testing…' : 'Test Connection'}
            </Button>
          )}
          {test.data && (
            <p className={`mt-2 text-xs ${test.data.ok ? toneText.success : 'text-destructive'}`}>{test.data.message}</p>
          )}
          {test.error && <p className="mt-2 text-xs text-destructive">{test.error.message}</p>}
        </PanelTemplate.Section>
      </PanelTemplate>
      <ConfirmDialog
        open={deleteTarget.open}
        onCancel={deleteTarget.cancel}
        verb="Delete"
        noun="integration"
        description={`"${item.name}" stops exporting. Pending events are preserved as disabled; existing MLflow runs are not deleted.`}
        error={deleteTarget.error}
        pending={remove.isPending}
        onConfirm={() => void deleteTarget.confirm(async target => {
          await remove.mutateAsync(target)
          void close()
        })}
      />
    </>
  )
}
