import { Link } from 'react-router-dom'
import { RefreshCw, Square, Trash2, X } from 'lucide-react'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import StatusBadge from '@/shared/components/StatusBadge'
import { useService, useStopService, useRestartService } from '@/features/serving/hooks'
import { useProjectId } from '@/lib/projectContext'

export function ServingDetailPanel({ name }: { name: string }) {
  const { close } = useSidePanel()
  const projectId = useProjectId()
  const { data: service, isLoading } = useService(name)
  const { mutateAsync: stopService } = useStopService()
  const { mutateAsync: restartService } = useRestartService()

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

  async function handleStop() {
    if (!confirm(`Stop service "${name}"?`)) return
    try { await stopService(name) } catch { /* no-op */ }
  }

  async function handleRestart() {
    try { await restartService(name) } catch { /* no-op */ }
  }

  async function handleDelete() {
    if (!confirm(`Delete service "${name}"?`)) return
    try { await stopService(name); void close() } catch { /* no-op */ }
  }

  return (
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
            <IconButton icon={<Square />} label="Stop" onClick={handleStop}
              className="text-destructive hover:bg-destructive/10" />
          )}
          {service.status === 'stopped' && (
            <IconButton icon={<Trash2 />} label="Delete" onClick={handleDelete}
              className="text-destructive hover:bg-destructive/10" />
          )}
          {closeBtn}
        </div>
      }
    >
      <PanelTemplate.Section title="Details">
        <dl className="grid grid-cols-2 gap-3">
          <div>
            <dt className="text-xs text-muted-foreground">Endpoint</dt>
            <dd className="mt-0.5">
              {service.endpoint ? (
                <a href={service.endpoint} target="_blank" rel="noreferrer"
                  className="font-mono text-xs text-primary hover:underline">
                  {service.endpoint}
                </a>
              ) : <span className="text-xs text-muted-foreground">—</span>}
            </dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Artifact</dt>
            <dd className="mt-0.5 font-mono text-xs">{service.artifact || '—'}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Namespace</dt>
            <dd className="mt-0.5 text-xs">{service.namespace || 'local'}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Source Run</dt>
            <dd className="mt-0.5 text-xs">
              {service.run_id ? (
                <Link to={`/projects/${projectId}/history`}
                  className="font-mono text-primary hover:underline">
                  {service.run_id.slice(0, 16)}…
                </Link>
              ) : '—'}
            </dd>
          </div>
          {service.pid > 0 && (
            <div>
              <dt className="text-xs text-muted-foreground">PID</dt>
              <dd className="mt-0.5 font-mono text-xs">{service.pid}</dd>
            </div>
          )}
          <div>
            <dt className="text-xs text-muted-foreground">Deployed</dt>
            <dd className="mt-0.5 text-xs">{new Date(service.created_at).toLocaleString()}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Updated</dt>
            <dd className="mt-0.5 text-xs">{new Date(service.updated_at).toLocaleString()}</dd>
          </div>
        </dl>
      </PanelTemplate.Section>

      <PanelTemplate.Section title="Service YAML">
        <pre className="overflow-x-auto rounded border border-border bg-muted/30 p-2 text-xs leading-6 text-muted-foreground">
          {service.yaml || '(empty)'}
        </pre>
      </PanelTemplate.Section>
    </PanelTemplate>
  )
}
