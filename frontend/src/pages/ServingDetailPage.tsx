import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { DataPage } from '@loykin/designkit'
import { getServing, stopServing, restartServing, type Service } from '@/features/serving/api'
import StatusBadge from '@/shared/components/StatusBadge'

export default function ServingDetailPage() {
  const { name } = useParams<{ name: string }>()
  const [service, setService] = useState<Service | null>(null)
  const [loading, setLoading] = useState(true)
  const navigate = useNavigate()

  async function load() {
    if (!name) return
    const svc = await getServing(name).catch(() => null)
    setService(svc)
    setLoading(false)
  }

  useEffect(() => {
    load()
    const t = setInterval(load, 5000)
    return () => clearInterval(t)
  }, [name])

  if (loading) {
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
    try { await stopServing(name); await load() } catch { /* no-op */ }
  }

  async function handleRestart() {
    if (!name) return
    try { await restartServing(name); await load() } catch { /* no-op */ }
  }

  async function handleDelete() {
    if (!name || !confirm(`Delete service "${name}"?`)) return
    try { await stopServing(name); navigate('/serving') } catch { /* no-op */ }
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
            <button type="button" onClick={handleRestart}
              className="rounded border border-border px-3 py-1.5 text-xs font-medium text-muted-foreground hover:text-foreground">
              Restart
            </button>
          )}
          {service.status !== 'stopped' && (
            <button type="button" onClick={handleStop}
              className="rounded border border-destructive/40 px-3 py-1.5 text-xs font-medium text-destructive hover:bg-destructive/10">
              Stop
            </button>
          )}
          {service.status === 'stopped' && (
            <button type="button" onClick={handleDelete}
              className="rounded border border-destructive/40 px-3 py-1.5 text-xs font-medium text-destructive hover:bg-destructive/10">
              Delete
            </button>
          )}
        </DataPage.Actions>
      </DataPage.Header>

      <DataPage.Content>
        {/* Metadata */}
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

        {/* YAML */}
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
