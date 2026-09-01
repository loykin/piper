// Read-only "is the stats (log/metric) backend up right now" diagnostic —
// the stats equivalent of StoragePage's Artifact Store Config section.
// Before this page, the only signal an operator had that ES/ClickHouse/
// InfluxDB was degraded was an amber banner that only rendered if someone
// happened to be looking at a specific run's Log Viewer (LogViewer.tsx). A
// system-level page means a degraded stats backend can actually be noticed
// rather than discovered days later by accident.
import { Badge } from '@/components/ui/badge'
import { DataBodyTemplate } from '@loykin/designkit'
import { useStatsCapabilities } from '@/features/runs/hooks'
import type { StatsCapabilities } from '@/features/runs/types'

function backendLabel(kind: string): string {
  switch (kind) {
    case 'database':     return 'Built-in database (fallback)'
    case 'elasticsearch': return 'Elasticsearch'
    case 'clickhouse':   return 'ClickHouse'
    case 'influxdb':     return 'InfluxDB'
    default:             return kind || '—'
  }
}

function statusVariant(stats: StatsCapabilities | undefined): 'default' | 'secondary' | 'destructive' {
  if (!stats) return 'secondary'
  if (!stats.healthy) return 'destructive'
  if (stats.degraded) return 'destructive'
  return 'default'
}

function statusLabel(stats: StatsCapabilities | undefined): string {
  if (!stats) return 'unknown'
  if (!stats.healthy) return 'unhealthy'
  if (stats.degraded) return 'degraded'
  return 'healthy'
}

export default function StatsBackendPage() {
  const query = useStatsCapabilities()
  const stats = query.data

  return (
    <DataBodyTemplate
      title="Stats Backend"
      status={query.isSuccess && <Badge variant={statusVariant(stats)}>{statusLabel(stats)}</Badge>}
    >
      <DataBodyTemplate.Group
        layout="stacked"
        title="Backend Status"
        description="Read-only. Which backend is currently serving run logs and metrics for this project, and whether it's reachable right now. Configured via stats.logs.url / stats.metrics.url in piper.yaml — see docs/log-metric-storage-backend.md."
      >
        {query.isPending && <p className="text-sm text-muted-foreground">Loading…</p>}

        {query.isError && (
          <p className="text-sm text-destructive">
            Couldn&apos;t load stats backend status:{' '}
            {query.error instanceof Error ? query.error.message : String(query.error)}
          </p>
        )}

        {stats && (
          <>
            <DataBodyTemplate.Field label="Logs backend" description="Serves run log queries and the Log Viewer's stream.">
              <span className="text-sm">{backendLabel(stats.logs_backend)}</span>
            </DataBodyTemplate.Field>

            <DataBodyTemplate.Field label="Metrics backend" description="Serves recorded step metrics and metric-based Alert Rules.">
              <span className="text-sm">{backendLabel(stats.metrics_backend)}</span>
            </DataBodyTemplate.Field>

            <DataBodyTemplate.Field label="Health" description="Live status, polled every few seconds.">
              <div className="space-y-1 text-sm">
                <p><span className="text-muted-foreground">Status: </span>{statusLabel(stats)}</p>
                <p>
                  <span className="text-muted-foreground">Pending (spooled) bytes: </span>
                  {stats.pending_bytes > 0
                    ? `${stats.pending_bytes} — writes are queued on disk waiting to flush to the backend.`
                    : '0'}
                </p>
                {stats.last_error && (
                  <p><span className="text-muted-foreground">Last error: </span>{stats.last_error}</p>
                )}
              </div>
            </DataBodyTemplate.Field>

            <DataBodyTemplate.Field label="Query capabilities" description="What the active backend combination supports.">
              <div className="flex flex-wrap gap-1.5">
                <Badge variant={stats.full_text_search ? 'default' : 'outline'}>Full-text search</Badge>
                <Badge variant={stats.time_range ? 'default' : 'outline'}>Time range</Badge>
                <Badge variant={stats.metric_key_filter ? 'default' : 'outline'}>Metric key filter</Badge>
              </div>
            </DataBodyTemplate.Field>
          </>
        )}
      </DataBodyTemplate.Group>
    </DataBodyTemplate>
  )
}
