import { useEffect, useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { getSchedule, setScheduleEnabled, deleteSchedule, type Schedule } from '../api'
import { useNavigate } from 'react-router-dom'
import RunDAG from '../components/RunDAG'

export default function ScheduleDetailPage() {
  const { id } = useParams<{ id: string }>()
  const [schedule, setSchedule] = useState<Schedule | null>(null)
  const [loading, setLoading] = useState(true)
  const navigate = useNavigate()

  useEffect(() => {
    if (!id) return
    getSchedule(id)
      .then(setSchedule)
      .catch(() => setSchedule(null))
      .finally(() => setLoading(false))
  }, [id])

  const selected = useMemo(() => null, [])

  if (loading) return <p className="text-sm text-gray-500">Loading...</p>
  if (!schedule) return <p className="text-sm text-gray-500">Schedule not found.</p>

  const isCron = schedule.schedule_type === 'cron'

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-3">
        <Link to="/schedules" className="text-sm text-gray-500 hover:text-gray-300">← Schedules</Link>
        <span className="text-gray-600">/</span>
        <span className="text-sm text-gray-300">{schedule.name}</span>
        <span
          className={`rounded px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide ${isCron ? 'border border-emerald-700 bg-emerald-900/30 text-emerald-300' : 'border border-violet-700 bg-violet-900/30 text-violet-300'}`}
        >
          {isCron ? 'Cron' : 'Once'}
        </span>
      </div>

      <section className="rounded-xl border border-gray-800 bg-gray-900 p-4">
        <dl className="grid grid-cols-2 gap-4 sm:grid-cols-3">
          {isCron && (
            <div>
              <dt className="text-xs text-gray-500">Cron</dt>
              <dd className="mt-1 font-mono text-sm text-gray-200">{schedule.cron_expr || '-'}</dd>
            </div>
          )}
          {!isCron && (
            <div>
              <dt className="text-xs text-gray-500">Scheduled At</dt>
              <dd className="mt-1 text-sm text-gray-200">{new Date(schedule.next_run_at).toLocaleString()}</dd>
            </div>
          )}
          <div>
            <dt className="text-xs text-gray-500">Next Run</dt>
            <dd className="mt-1 text-sm text-gray-200">
              {!isCron && !schedule.enabled ? 'Done' : new Date(schedule.next_run_at).toLocaleString()}
            </dd>
          </div>
          <div>
            <dt className="text-xs text-gray-500">Last Run</dt>
            <dd className="mt-1 text-sm text-gray-200">
              {schedule.last_run_at ? new Date(schedule.last_run_at).toLocaleString() : '-'}
            </dd>
          </div>
          <div>
            <dt className="text-xs text-gray-500">Status</dt>
            <dd className="mt-1 text-sm text-gray-200">{schedule.enabled ? 'Active' : isCron ? 'Disabled' : 'Done'}</dd>
          </div>
          <div>
            <dt className="text-xs text-gray-500">Created</dt>
            <dd className="mt-1 text-sm text-gray-200">{new Date(schedule.created_at).toLocaleString()}</dd>
          </div>
        </dl>

        <div className="mt-4 flex items-center gap-2">
          {isCron && (
            <button
              type="button"
              onClick={async () => {
                try {
                  await setScheduleEnabled(schedule.id, !schedule.enabled)
                  const updated = await getSchedule(schedule.id)
                  setSchedule(updated)
                } catch { /* no-op */ }
              }}
              className={`rounded border px-3 py-1.5 text-xs font-medium ${schedule.enabled ? 'border-green-700 bg-green-900/20 text-green-300' : 'border-gray-700 bg-gray-900 text-gray-400'}`}
            >
              {schedule.enabled ? 'Enabled — click to disable' : 'Disabled — click to enable'}
            </button>
          )}
          <button
            type="button"
            onClick={async () => {
              if (!confirm(`Delete schedule "${schedule.name}"?`)) return
              try { await deleteSchedule(schedule.id); navigate('/schedules') } catch { /* no-op */ }
            }}
            className="rounded border border-red-900 bg-red-950/20 px-3 py-1.5 text-xs font-medium text-red-400 hover:bg-red-900/30"
          >
            Delete
          </button>
        </div>
      </section>

      <RunDAG
        pipelineYaml={schedule.pipeline_yaml}
        steps={[]}
        selected={selected}
        onSelectStep={() => {}}
      />

      <section className="overflow-hidden rounded-xl border border-gray-800 bg-gray-950">
        <div className="border-b border-gray-800 bg-gray-900 px-4 py-3">
          <h2 className="text-sm font-semibold uppercase tracking-wider text-gray-300">Pipeline YAML</h2>
        </div>
        <pre className="overflow-x-auto p-4 text-xs leading-6 text-gray-300">{schedule.pipeline_yaml || '(empty)'}</pre>
      </section>
    </div>
  )
}

