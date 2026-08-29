import { ExternalLink } from 'lucide-react'
import { PanelTemplate } from '@loykin/designkit'
import StatusBadge from '@/shared/components/StatusBadge'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { useMLflowRunLinks } from '../hooks'

export function MLflowRunLinks({ runId }: { runId: string }) {
  const query = useMLflowRunLinks(runId)
  if (!query.isLoading && !query.isError && (query.data?.length ?? 0) === 0) return null
  return <PanelTemplate title="External Tracking" className="mb-4 h-auto rounded-lg border border-border">
    {query.isError ? <QueryErrorNotice message="Failed to load MLflow sync status" error={query.error} onRetry={() => void query.refetch()} /> : query.isLoading ? <p className="text-sm text-muted-foreground">Loading MLflow status…</p> : query.data?.map(link => <div key={link.integration_id} className="space-y-2 border-b border-border py-3 last:border-0">
      <div className="flex items-center justify-between"><StatusBadge status={link.sync_status} />{link.mlflow_run_url && <a href={link.mlflow_run_url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-sm text-primary hover:underline">Open in MLflow <ExternalLink className="size-3.5" /></a>}</div>
      <p className="font-mono text-xs text-muted-foreground">{link.mlflow_run_id || 'MLflow run pending'}</p>
      {link.last_synced_at && <p className="text-xs text-muted-foreground">Last synced {new Date(link.last_synced_at).toLocaleString()}</p>}
      {link.last_error_code && <p className="text-xs text-destructive">{link.last_error_code}: {link.last_error_message}</p>}
    </div>)}
  </PanelTemplate>
}
