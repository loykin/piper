import { Link, useNavigate, useParams } from 'react-router-dom'
import { RefreshCw, Square, Trash2 } from 'lucide-react'
import { DataPage } from '@loykin/designkit'
import { IconButton } from '@/components/ui/icon-button'
import StatusBadge from '@/shared/components/StatusBadge'
import { useService, useStopService, useRestartService } from '@/features/serving/hooks'

export default function ServingDetailPage() {
  const { name } = useParams<{ name: string }>()
  const navigate = useNavigate()
  const { data: service, isLoading } = useService(name!)
  const { mutateAsync: stopService } = useStopService()
  const { mutateAsync: restartService } = useRestartService()

  if (isLoading) {
    return (
      <DataPage>
        <DataPage.Content>
          <p className="text-sm text-muted-foreground">Loading…</p>
        </DataPage.Content>
      </DataPage>
    )
  }

  if (!service) {
    return (
      <DataPage>
        <DataPage.Content>
          <p className="text-sm text-muted-foreground">Service not found.</p>
        </DataPage.Content>
      </DataPage>
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
    try { await stopService(name); navigate('/serving') } catch { /* no-op */ }
  }

  return (
    <DataPage>
      <DataPage.Header>
        <DataPage.TitleBlock
          breadcrumb={<Link to="/serving" className="hover:text-foreground transition-colors">← Serving</Link>}
          title={service.name}
          description="ModelService deployment"
        />
        <DataPage.Actions>
          <StatusBadge status={service.status} />
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
        </DataPage.Actions>
      </DataPage.Header>

      <DataPage.Content>
        <DataPage.Group surface="bordered" className="mb-4">
          <div className="p-4">
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
          </div>
        </DataPage.Group>

        <DataPage.Group surface="bordered">
          <DataPage.GroupHeader title="Service YAML" className="px-4 pt-3" />
          <pre className="overflow-x-auto px-4 pb-4 text-xs leading-6 text-muted-foreground">
            {service.yaml || '(empty)'}
          </pre>
        </DataPage.Group>
      </DataPage.Content>
    </DataPage>
  )
}
