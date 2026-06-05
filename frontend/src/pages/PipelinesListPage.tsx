import { useEffect, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { CalendarClock, Play, Plus, Trash2, X } from 'lucide-react'
import { DataPage } from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { IconButton } from '@/components/ui/icon-button'
import { listPipelines, deletePipeline, runPipeline, deployPipeline, type PipelineTemplate } from '@/features/pipelines/api'

function relativeTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime()
  const min = Math.floor(diff / 60000)
  if (min < 1) return 'just now'
  if (min < 60) return `${min}m ago`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr}h ago`
  return `${Math.floor(hr / 24)}d ago`
}

interface DeployModalState {
  template: PipelineTemplate
  cron: string
  enabled: boolean
}

export default function PipelinesListPage() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const filterName = searchParams.get('name') ?? ''

  const [templates, setTemplates] = useState<PipelineTemplate[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [actionError, setActionError] = useState('')
  const [deployModal, setDeployModal] = useState<DeployModalState | null>(null)
  const [deploying, setDeploying] = useState(false)
  const intervalRef = useRef<ReturnType<typeof setInterval>>(0 as unknown as ReturnType<typeof setInterval>)

  const load = () =>
    listPipelines(filterName || undefined)
      .then(data => { setTemplates(data); setLoadError('') })
      .catch(() => setLoadError('Failed to load pipeline templates.'))
      .finally(() => setLoading(false))

  useEffect(() => {
    load()
    intervalRef.current = setInterval(load, 10000)
    return () => clearInterval(intervalRef.current)
  }, [filterName])

  async function handleRun(t: PipelineTemplate) {
    setActionError('')
    try {
      const result = await runPipeline(t.id)
      navigate(`/runs/${result.id}`)
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }

  async function handleDelete(t: PipelineTemplate) {
    if (!confirm(`Delete pipeline template "${t.name}" (${t.id.slice(0, 8)}…)? This also deletes the S3 snapshot.`)) return
    setActionError('')
    try {
      await deletePipeline(t.id)
      await load()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }

  async function handleDeploy() {
    if (!deployModal) return
    setDeploying(true)
    setActionError('')
    try {
      await deployPipeline(deployModal.template.id, { cron: deployModal.cron, enabled: deployModal.enabled })
      setDeployModal(null)
      navigate('/schedules')
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    } finally {
      setDeploying(false)
    }
  }

  // Group by name, sorted by most recent first within each group
  const grouped = templates.reduce<Map<string, PipelineTemplate[]>>((acc, t) => {
    const list = acc.get(t.name) ?? []
    list.push(t)
    acc.set(t.name, list)
    return acc
  }, new Map())

  return (
    <DataPage>
      <DataPage.Header>
        <DataPage.TitleBlock
          title="Pipeline Templates"
          description="Each submit creates a new versioned snapshot. Deploy to schedule or run on demand."
        />
        <DataPage.Actions>
          <Button size="sm" onClick={() => navigate('/pipelines/editor')}>
            <Plus size={14} className="mr-1.5" /> New Pipeline
          </Button>
        </DataPage.Actions>
      </DataPage.Header>

      <DataPage.Content>
        {loadError && <p className="mb-4 text-sm text-destructive">{loadError}</p>}
        {actionError && (
          <div className="mb-4 flex items-center gap-2 rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
            {actionError}
            <button type="button" onClick={() => setActionError('')} className="ml-auto"><X size={14} /></button>
          </div>
        )}

        {loading ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : grouped.size === 0 ? (
          <div className="py-16 text-center">
            <p className="text-sm text-muted-foreground">No pipeline templates yet.</p>
            <Button variant="outline" size="sm" className="mt-4" onClick={() => navigate('/pipelines/editor')}>
              <Plus size={14} className="mr-1.5" /> Create your first pipeline
            </Button>
          </div>
        ) : (
          <div className="space-y-6">
            {Array.from(grouped.entries()).map(([name, rows]) => (
              <DataPage.Group key={name} surface="bordered">
                <DataPage.GroupHeader
                  title={name}
                  description={`${rows.length} submission${rows.length !== 1 ? 's' : ''}`}
                  className="px-4 pt-3"
                />
                <div className="px-4 pb-4">
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
                              <IconButton icon={<Play />} label="Run" onClick={() => handleRun(t)} />
                              <IconButton
                                icon={<CalendarClock />}
                                label="Deploy to schedule"
                                onClick={() => setDeployModal({ template: t, cron: '0 2 * * *', enabled: true })}
                              />
                              <IconButton
                                icon={<Trash2 />}
                                label="Delete"
                                onClick={() => handleDelete(t)}
                                className="text-destructive hover:bg-destructive/10"
                              />
                            </div>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </DataPage.Group>
            ))}
          </div>
        )}
      </DataPage.Content>

      {/* Deploy modal */}
      {deployModal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
          <div className="w-full max-w-sm rounded-xl border border-border bg-card p-6 shadow-xl">
            <div className="mb-4 flex items-center justify-between">
              <h2 className="text-sm font-semibold">Deploy to Schedule</h2>
              <button type="button" onClick={() => setDeployModal(null)} className="rounded p-1 hover:bg-accent">
                <X size={14} />
              </button>
            </div>
            <p className="mb-4 text-xs text-muted-foreground">
              Creates a new schedule with a snapshot of <strong>{deployModal.template.name}</strong>.
              The schedule is independent — updating the template later does not affect it.
            </p>
            <div className="mb-4">
              <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Cron Expression</label>
              <Input
                value={deployModal.cron}
                onChange={e => setDeployModal(m => m ? { ...m, cron: e.target.value } : null)}
                placeholder="0 2 * * *"
              />
            </div>
            <div className="mb-4 flex items-center gap-2">
              <input
                id="deploy-enabled"
                type="checkbox"
                checked={deployModal.enabled}
                onChange={e => setDeployModal(m => m ? { ...m, enabled: e.target.checked } : null)}
                className="rounded"
              />
              <label htmlFor="deploy-enabled" className="text-xs">Enable immediately</label>
            </div>
            {actionError && <p className="mb-3 text-xs text-destructive">{actionError}</p>}
            <div className="flex justify-end gap-2">
              <Button variant="outline" size="sm" onClick={() => setDeployModal(null)} disabled={deploying}>Cancel</Button>
              <Button size="sm" onClick={handleDeploy} disabled={deploying || !deployModal.cron.trim()}>
                <CalendarClock size={14} className="mr-1.5" />
                {deploying ? 'Deploying…' : 'Deploy'}
              </Button>
            </div>
          </div>
        </div>
      )}
    </DataPage>
  )
}
