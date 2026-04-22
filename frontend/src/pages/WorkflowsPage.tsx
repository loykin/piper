import { useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { listRuns, listSchedules, setScheduleEnabled, type Run, type Schedule } from '../api'
import StatusBadge from '../components/StatusBadge'

interface PipelineSummary {
  name: string
  latestRunId: string
  latestStatus: Run['status']
  latestStartedAt: string
  runCount: number
}

interface PipelineListItem {
  key: string
  name: string
  type: 'manual' | 'cron' | 'scheduled'
  sortAt: number
  subtitle: string
  latestStatus?: Run['status']
  latestRunId?: string
  scheduleId?: string
  scheduleEnabled?: boolean
}

export default function WorkflowsPage() {
  const [runs, setRuns] = useState<Run[]>([])
  const [schedules, setSchedules] = useState<Schedule[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const navigate = useNavigate()
  const intervalRef = useRef<ReturnType<typeof setInterval>>(0 as unknown as ReturnType<typeof setInterval>)

  const load = () =>
    Promise.allSettled([listRuns(), listSchedules()])
      .then((results) => {
        const [runsResult, schedulesResult] = results
        let hasError = false

        if (runsResult.status === 'fulfilled') {
          setRuns(Array.isArray(runsResult.value) ? runsResult.value : [])
        } else {
          hasError = true
        }

        if (schedulesResult.status === 'fulfilled') {
          setSchedules(Array.isArray(schedulesResult.value) ? schedulesResult.value : [])
        } else {
          hasError = true
        }

        setLoadError(hasError ? 'Some data failed to load. Check backend/proxy status.' : '')
      })
      .finally(() => setLoading(false))

  useEffect(() => {
    load()
    intervalRef.current = setInterval(load, 3000)
    return () => clearInterval(intervalRef.current)
  }, [])

  const manualPipelines = useMemo(() => {
    const map = new Map<string, PipelineSummary>()

    for (const run of runs) {
      if (run.status === 'scheduled') {
        continue
      }
      const key = run.pipeline_name || 'unnamed-pipeline'
      const existing = map.get(key)

      if (!existing) {
        map.set(key, {
          name: key,
          latestRunId: run.id,
          latestStatus: run.status,
          latestStartedAt: run.started_at,
          runCount: 1,
        })
        continue
      }

      existing.runCount += 1
      if (new Date(run.started_at).getTime() > new Date(existing.latestStartedAt).getTime()) {
        existing.latestRunId = run.id
        existing.latestStatus = run.status
        existing.latestStartedAt = run.started_at
      }
    }

    return Array.from(map.values()).sort(
      (a, b) => new Date(b.latestStartedAt).getTime() - new Date(a.latestStartedAt).getTime(),
    )
  }, [runs])

  const items = useMemo<PipelineListItem[]>(() => {
    const out: PipelineListItem[] = []

    for (const p of manualPipelines) {
      out.push({
        key: `manual:${p.name}`,
        name: p.name,
        type: 'manual',
        sortAt: new Date(p.latestStartedAt).getTime(),
        subtitle: `Last run: ${new Date(p.latestStartedAt).toLocaleString()} · Total runs: ${p.runCount}`,
        latestStatus: p.latestStatus,
        latestRunId: p.latestRunId,
      })
    }

    for (const s of schedules) {
      out.push({
        key: `cron:${s.id}`,
        name: s.name,
        type: 'cron',
        sortAt: new Date(s.next_run_at).getTime(),
        subtitle: `${s.cron_expr} · next: ${new Date(s.next_run_at).toLocaleString()}`,
        scheduleId: s.id,
        scheduleEnabled: s.enabled,
      })
    }

    for (const r of runs) {
      if (r.status !== 'scheduled') {
        continue
      }
      out.push({
        key: `scheduled:${r.id}`,
        name: r.pipeline_name || 'unnamed-pipeline',
        type: 'scheduled',
        sortAt: new Date(r.scheduled_at ?? r.started_at).getTime(),
        subtitle: `One-time schedule · run at: ${new Date(r.scheduled_at ?? r.started_at).toLocaleString()}`,
        latestStatus: r.status,
        latestRunId: r.id,
      })
    }

    return out.sort((a, b) => b.sortAt - a.sortAt)
  }, [manualPipelines, schedules])

  return (
    <div className="space-y-6">
      <section className="rounded-xl border border-gray-800 bg-gray-900 p-6">
        <div className="flex items-center justify-between gap-4">
          <div>
            <h1 className="text-lg font-semibold text-gray-100">Pipelines</h1>
            <p className="mt-1 text-sm text-gray-400">Manual and cron pipelines are shown in one list, separated by type badges.</p>
          </div>
          <Link
            to="/pipelines/create"
            className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-indigo-500"
          >
            + Create Pipeline
          </Link>
        </div>
      </section>

      <section className="overflow-hidden rounded-xl border border-gray-800 bg-gray-950">
        <div className="border-b border-gray-800 bg-gray-900 px-4 py-3">
          <h2 className="text-sm font-semibold uppercase tracking-wider text-gray-300">Pipeline List</h2>
        </div>

        {loadError && <div className="border-b border-amber-900 bg-amber-950/30 px-4 py-2 text-xs text-amber-300">{loadError}</div>}

        {loading ? (
          <div className="px-4 py-8 text-sm text-gray-500">Loading...</div>
        ) : items.length === 0 ? (
          <div className="px-4 py-8 text-sm text-gray-500">No pipelines yet. Create one to start.</div>
        ) : (
          <ul className="divide-y divide-gray-800">
            {items.map((item) => (
              <li key={item.key} className="px-4 py-3">
                <div className="flex flex-wrap items-center justify-between gap-4">
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <p className="truncate text-sm font-medium text-gray-100">{item.name}</p>
                      <span
                        className={`rounded px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide ${item.type === 'cron' ? 'border border-emerald-700 bg-emerald-900/30 text-emerald-300' : item.type === 'scheduled' ? 'border border-violet-700 bg-violet-900/30 text-violet-300' : 'border border-indigo-700 bg-indigo-900/30 text-indigo-300'}`}
                      >
                        {item.type === 'cron' ? 'Cron' : item.type === 'scheduled' ? 'Scheduled' : 'Manual'}
                      </span>
                    </div>
                    <p className="mt-1 text-xs text-gray-500">{item.subtitle}</p>
                  </div>

                  <div className="flex items-center gap-3">
                    {(item.type === 'manual' || item.type === 'scheduled') && item.latestStatus && item.latestRunId && (
                      <>
                        <StatusBadge status={item.latestStatus} />
                        <button
                          type="button"
                          onClick={() => navigate(`/runs/${item.latestRunId}`)}
                          className="rounded-md border border-gray-700 bg-gray-900 px-2 py-1 text-xs text-gray-300 hover:bg-gray-800"
                        >
                          Open Latest Run
                        </button>
                      </>
                    )}
                    {item.type === 'cron' && item.scheduleId && (
                      <button
                        type="button"
                        onClick={async () => {
                          try {
                            await setScheduleEnabled(item.scheduleId!, !item.scheduleEnabled)
                            await load()
                          } catch {
                            // no-op
                          }
                        }}
                        className={`rounded-md border px-2 py-1 text-xs ${item.scheduleEnabled ? 'border-green-700 bg-green-900/30 text-green-300' : 'border-gray-700 bg-gray-900 text-gray-400'}`}
                      >
                        {item.scheduleEnabled ? 'Enabled' : 'Disabled'}
                      </button>
                    )}
                  </div>
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}
