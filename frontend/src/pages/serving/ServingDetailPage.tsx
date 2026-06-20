import { Link, useNavigate, useParams } from 'react-router-dom'
import { useProjectId } from '@/lib/projectContext'
import { RefreshCw, Square, Trash2 } from 'lucide-react'
import { DetailBodyTemplate } from '@loykin/designkit'
import { IconButton } from '@/components/ui/icon-button'
import StatusBadge from '@/shared/components/StatusBadge'
import { useService, useStopService, useRestartService } from '@/features/serving/hooks'

export default function ServingDetailPage() {
  const { name } = useParams<{ name: string }>()
  const navigate = useNavigate()
  const projectId = useProjectId()
  const { data: service, isLoading } = useService(name!)
  const { mutateAsync: stopService } = useStopService()
  const { mutateAsync: restartService } = useRestartService()

  if (isLoading) {
    return (
      <DetailBodyTemplate title="Loading…">
        <DetailBodyTemplate.Section>
          <p className="text-sm text-muted-foreground">Loading…</p>
        </DetailBodyTemplate.Section>
      </DetailBodyTemplate>
    )
  }

  if (!service) {
    return (
      <DetailBodyTemplate title="Not Found">
        <DetailBodyTemplate.Section>
          <p className="text-sm text-muted-foreground">Service not found.</p>
        </DetailBodyTemplate.Section>
      </DetailBodyTemplate>
    )
  }

  async function handleStop() {
    if (!name || !confirm(`Stop service "${name}"?`)) return
    try { await stopService(name) } catch { /* no-op */ }
  }

  async function handleRestart() {
    if (!name) return
    try { await restartService(name) } catch { /* no-op */ }
  }

  async function handleDelete() {
    if (!name || !confirm(`Delete service "${name}"?`)) return
    try { await stopService(name); navigate(`/projects/${projectId}/serving`) } catch { /* no-op */ }
  }

  return (
    <DetailBodyTemplate
      eyebrow={<Link to={`/projects/${projectId}/serving`} className="hover:text-foreground transition-colors">← Serving</Link>}
      title={service.name}
      description="ModelService deployment"
      status={<StatusBadge status={service.status} />}
      actions={
        <div className="flex items-center gap-0.5">
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
        </div>
      }
    >
      <DetailBodyTemplate.Section title="Details" surface="bordered">
        <dl className="grid grid-cols-2 gap-4 sm:grid-cols-3">
          <div>
            <dt className="text-xs text-muted-foreground">Endpoint</dt>
            <dd className="mt-1">
              {service.endpoint ? (
                <a href={service.endpoint} target="_blank" rel="noreferrer"
                  className="font-mono text-sm text-primary hover:underline">
                  {service.endpoint}
                </a>
              ) : <span className="text-sm text-muted-foreground">—</span>}
            </dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Artifact</dt>
            <dd className="mt-1 font-mono text-sm">{service.artifact || '—'}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Namespace</dt>
            <dd className="mt-1 text-sm">{service.namespace || 'local'}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Source Run</dt>
            <dd className="mt-1 text-sm">
              {service.run_id ? (
                <Link to={`/runs/${service.run_id}`} className="font-mono text-primary hover:underline">
                  {service.run_id}
                </Link>
              ) : '—'}
            </dd>
          </div>
          {service.pid > 0 && (
            <div>
              <dt className="text-xs text-muted-foreground">PID</dt>
              <dd className="mt-1 font-mono text-sm">{service.pid}</dd>
            </div>
          )}
          <div>
            <dt className="text-xs text-muted-foreground">Deployed</dt>
            <dd className="mt-1 text-sm">{new Date(service.created_at).toLocaleString()}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Updated</dt>
            <dd className="mt-1 text-sm">{new Date(service.updated_at).toLocaleString()}</dd>
          </div>
        </dl>
      </DetailBodyTemplate.Section>

      <DetailBodyTemplate.Section title="Service YAML" surface="bordered">
        <pre className="overflow-x-auto text-xs leading-6 text-muted-foreground">
          {service.yaml || '(empty)'}
        </pre>
      </DetailBodyTemplate.Section>
    </DetailBodyTemplate>
  )
}
