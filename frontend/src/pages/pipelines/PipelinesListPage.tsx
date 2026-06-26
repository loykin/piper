import { useState } from 'react'
import { useNavigate, useSearchParams } from '@/lib/router'
import { useProjectId } from '@/lib/projectContext'
import { CalendarClock, Play, Plus, Trash2, X } from 'lucide-react'
import { DataBodyTemplate } from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { usePipelines, useDeletePipeline, useRunPipeline } from '@/features/pipelines/hooks'
import { DeployModal } from '@/features/pipelines/components/DeployModal'
import type { PipelineTemplate } from '@/features/pipelines/api'

function relativeTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime()
  const min = Math.floor(diff / 60000)
  if (min < 1) return 'just now'
  if (min < 60) return `${min}m ago`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr}h ago`
  return `${Math.floor(hr / 24)}d ago`
}

export default function PipelinesListPage() {
  const navigate = useNavigate()
  const projectId = useProjectId()
  const [searchParams] = useSearchParams()
  const filterName = searchParams.get('name') ?? ''

  const { data: templates = [], isLoading, error: loadError } = usePipelines(filterName || undefined)
  const { mutateAsync: deletePipeline } = useDeletePipeline()
  const { mutateAsync: runPipeline } = useRunPipeline()

  const [deployTarget, setDeployTarget] = useState<PipelineTemplate | null>(null)
  const [deployCron, setDeployCron] = useState('0 2 * * *')
  const [deployEnabled, setDeployEnabled] = useState(true)
  const [actionError, setActionError] = useState('')

  async function handleRun(t: PipelineTemplate) {
    setActionError('')
    try {
      const result = await runPipeline({ id: t.id })
      navigate(`/projects/${projectId}/runs/${result.id}`)
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }

  async function handleDelete(t: PipelineTemplate) {
    if (!confirm(`Delete pipeline template "${t.name}" (${t.id.slice(0, 8)}…)? This also deletes the S3 snapshot.`)) return
    setActionError('')
    try {
      await deletePipeline(t.id)
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }

  function openDeploy(t: PipelineTemplate) {
    setDeployTarget(t)
    setDeployCron('0 2 * * *')
    setDeployEnabled(true)
    setActionError('')
  }

  // Group by name, sorted by most recent first within each group
  const grouped = templates.reduce<Map<string, PipelineTemplate[]>>((acc, t) => {
    const list = acc.get(t.name) ?? []
    list.push(t)
    acc.set(t.name, list)
    return acc
  }, new Map())

  return (
    <DataBodyTemplate
      title="Pipeline Templates"
      description="Each submit creates a new versioned snapshot. Deploy to schedule or run on demand."
      actions={
        <Button size="sm" onClick={() => navigate(`/projects/${projectId}/pipelines/editor`)}>
          <Plus size={14} className="mr-1.5" /> New Pipeline
        </Button>
      }
    >
      <DataBodyTemplate.Body>
        {loadError && <p className="mb-4 text-sm text-destructive">Failed to load pipeline templates.</p>}
        {actionError && (
          <div className="mb-4 flex items-center gap-2 rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
            {actionError}
            <button type="button" onClick={() => setActionError('')} className="ml-auto"><X size={14} /></button>
          </div>
        )}

        {isLoading ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : grouped.size === 0 ? (
          <div className="py-16 text-center">
            <p className="text-sm text-muted-foreground">No pipeline templates yet.</p>
            <Button variant="outline" size="sm" className="mt-4" onClick={() => navigate(`/projects/${projectId}/pipelines/editor`)}>
              <Plus size={14} className="mr-1.5" /> Create your first pipeline
            </Button>
          </div>
        ) : (
          <div className="space-y-4">
            {Array.from(grouped.entries()).map(([name, rows]) => (
              <DataBodyTemplate.Group
                key={name}
                variant="bordered"
                title={name}
                description={`${rows.length} submission${rows.length !== 1 ? 's' : ''}`}
              >
                <table className="w-full text-xs">
                  <thead>
                    <tr className="border-b border-border text-muted-foreground">
                      <th className="py-2 text-left font-medium">ID</th>
                      <th className="py-2 text-left font-medium">Snapshot</th>
                      <th className="py-2 text-left font-medium">Volume</th>
                      <th className="py-2 text-left font-medium">Submitted</th>
                      <th className="py-2 text-right font-medium">Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map(t => (
                      <tr key={t.id} className="border-b border-border/40 last:border-0">
                        <td className="py-2 font-mono text-foreground">{t.id.slice(0, 8)}…</td>
                        <td className="py-2 font-mono text-muted-foreground">{t.snapshot_id.slice(0, 8)}…</td>
                        <td className="py-2 text-muted-foreground">{t.volume_id || '—'}</td>
                        <td className="py-2 text-muted-foreground" title={t.created_at}>{relativeTime(t.created_at)}</td>
                        <td className="py-2">
                          <div className="flex items-center justify-end gap-0.5">
                            <IconButton icon={<Play />} label="Run" onClick={() => void handleRun(t)} />
                            <IconButton
                              icon={<CalendarClock />}
                              label="Deploy to schedule"
                              onClick={() => openDeploy(t)}
                            />
                            <IconButton
                              icon={<Trash2 />}
                              label="Delete"
                              onClick={() => void handleDelete(t)}
                              className="text-destructive hover:bg-destructive/10"
                            />
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </DataBodyTemplate.Group>
            ))}
          </div>
        )}
      </DataBodyTemplate.Body>

      <DeployModal
        template={deployTarget}
        cron={deployCron}
        enabled={deployEnabled}
        onCronChange={setDeployCron}
        onEnabledChange={setDeployEnabled}
        onClose={() => setDeployTarget(null)}
        onDeployed={() => navigate(`/projects/${projectId}/schedules`)}
        error={actionError}
      />
    </DataBodyTemplate>
  )
}
