import { Link } from '@/lib/router'
import { RefreshCw, Square, Trash2 } from 'lucide-react'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { IconButton } from '@/components/ui/icon-button'
import StatusBadge from '@/shared/components/StatusBadge'
import { useService, useStopService, useRestartService } from '@/features/serving/hooks'
import { useProjectId } from '@/features/projects/context'
import { useConfirmAction } from '@/shared/hooks/useConfirmAction'
import { errorMessage, fmtDate } from '@/lib/format'
import { PanelCloseButton, PanelPlaceholder } from '@/shared/components/PanelPlaceholder'
import { ConfirmDialog } from '@/shared/components/ConfirmDialog'

export function ServingDetailPanel({ name }: { name: string }) {
  const { close } = useSidePanel()
  const projectId = useProjectId()
  const query = useService(name)
  const service = query.data
  const { mutateAsync: stopService, isPending: stopping } = useStopService()
  const restart = useRestartService()
  const confirmation = useConfirmAction<'stop' | 'delete'>()

  if (!service) return <PanelPlaceholder query={query} noun="service" />

  function handleConfirm() {
    return confirmation.confirm(async () => {
      await stopService(name)
      // A stopped service leaves the list (it moves to Serving History).
      void close()
    })
  }

  return (
    <>
    <PanelTemplate
      eyebrow="Service"
      title={service.name}
      status={<StatusBadge status={service.status} />}
      actions={
        <div className="flex items-center gap-1">
          {service.status === 'running' && (
            <IconButton icon={<RefreshCw />} label="Restart" onClick={() => restart.mutate(name)} />
          )}
          {service.status !== 'stopped' && (
            <IconButton icon={<Square />} label="Stop" onClick={() => confirmation.requestAction('stop')}
              className="text-destructive hover:bg-destructive/10" />
          )}
          {service.status === 'stopped' && (
            <IconButton icon={<Trash2 />} label="Delete" onClick={() => confirmation.requestAction('delete')}
              className="text-destructive hover:bg-destructive/10" />
          )}
          <PanelCloseButton />
        </div>
      }
    >
      {restart.isError && (
        <PanelTemplate.Section>
          <p className="text-xs text-destructive">{errorMessage(restart.error)}</p>
        </PanelTemplate.Section>
      )}
      <PanelTemplate.Section title="Details">
        <dl className="space-y-2">
          <PanelTemplate.Row label="Endpoint">
            {service.endpoint ? (
              <a href={service.endpoint} target="_blank" rel="noreferrer" className="text-primary hover:underline">
                {service.endpoint}
              </a>
            ) : '—'}
          </PanelTemplate.Row>
          <PanelTemplate.Row label="Artifact">{service.artifact || '—'}</PanelTemplate.Row>
          <PanelTemplate.Row label="Namespace">{service.namespace || 'local'}</PanelTemplate.Row>
          <PanelTemplate.Row label="Source Run">
            {service.run_id ? (
              <Link to={`/projects/${projectId}/history`} className="text-primary hover:underline">
                {service.run_id.slice(0, 16)}…
              </Link>
            ) : '—'}
          </PanelTemplate.Row>
          {service.pid > 0 && (
            <PanelTemplate.Row label="PID">{service.pid}</PanelTemplate.Row>
          )}
          <PanelTemplate.Row label="Deployed">{fmtDate(service.created_at)}</PanelTemplate.Row>
          <PanelTemplate.Row label="Updated">{fmtDate(service.updated_at)}</PanelTemplate.Row>
        </dl>
      </PanelTemplate.Section>

      <PanelTemplate.Section title="Service YAML">
        <pre className="overflow-x-auto rounded border border-border bg-muted/30 p-2 text-xs leading-6 text-muted-foreground">
          {service.yaml || '(empty)'}
        </pre>
      </PanelTemplate.Section>
    </PanelTemplate>

    <ConfirmDialog
      open={confirmation.open}
      onCancel={confirmation.cancel}
      verb={confirmation.action === 'stop' ? 'Stop' : 'Delete'}
      noun="service"
      description={`"${name}" will stop serving requests immediately.`}
      error={confirmation.error}
      pending={stopping}
      onConfirm={() => void handleConfirm()}
    />
    </>
  )
}
