import { Link } from '@/lib/router'
import { RefreshCw, Square, Trash2, X } from 'lucide-react'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import StatusBadge from '@/shared/components/StatusBadge'
import { useService, useStopService, useRestartService } from '@/features/serving/hooks'
import { useProjectId } from '@/features/projects/context'
import { useConfirmAction } from '@/shared/hooks/useConfirmAction'
import { fmtDate } from '@/lib/format'
import { ConfirmDialog } from '@/shared/components/ConfirmDialog'

export function ServingDetailPanel({ name }: { name: string }) {
  const { close } = useSidePanel()
  const projectId = useProjectId()
  const { data: service, isLoading } = useService(name)
  const { mutateAsync: stopService, isPending: stopping } = useStopService()
  const { mutateAsync: restartService } = useRestartService()
  const { action: confirmAction, requestAction: requestConfirm, cancel: cancelConfirm } = useConfirmAction<'stop' | 'delete'>()

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

  if (!service) {
    return (
      <PanelTemplate title="Not Found" actions={closeBtn}>
        <PanelTemplate.Section>
          <p className="text-xs text-muted-foreground">Service not found.</p>
        </PanelTemplate.Section>
      </PanelTemplate>
    )
  }

  async function handleRestart() {
    try { await restartService(name) } catch { /* no-op */ }
  }

  async function handleConfirm() {
    try {
      await stopService(name)
      // A stopped service leaves the list (it moves to Serving History), so
      // the panel would otherwise keep showing it as running.
      void close()
    } catch { /* no-op */ } finally {
      cancelConfirm()
    }
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
            <IconButton icon={<RefreshCw />} label="Restart" onClick={handleRestart} />
          )}
          {service.status !== 'stopped' && (
            <IconButton icon={<Square />} label="Stop" onClick={() => requestConfirm('stop')}
              className="text-destructive hover:bg-destructive/10" />
          )}
          {service.status === 'stopped' && (
            <IconButton icon={<Trash2 />} label="Delete" onClick={() => requestConfirm('delete')}
              className="text-destructive hover:bg-destructive/10" />
          )}
          {closeBtn}
        </div>
      }
    >
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
      open={confirmAction != null}
      onCancel={cancelConfirm}
      title={confirmAction === 'stop' ? 'Stop this service?' : 'Delete this service?'}
      description={`"${name}" will stop serving requests immediately.`}
      pending={stopping}
      confirmLabel={stopping ? 'Working…' : confirmAction === 'stop' ? 'Stop service' : 'Delete service'}
      onConfirm={() => void handleConfirm()}
    />
    </>
  )
}
