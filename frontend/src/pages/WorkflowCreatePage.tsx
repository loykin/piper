import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { createSchedule } from '../api'

type ScheduleType = 'immediate' | 'once' | 'cron'

const EXAMPLE_YAML = `apiVersion: piper/v1
kind: Pipeline
metadata:
  name: my-pipeline
spec:
  steps:
    - name: hello
      run:
        command: [echo, "hello from piper"]
    - name: world
      depends_on: [hello]
      run:
        command: [echo, "world!"]
`

function applyPipelineName(yaml: string, name: string): string {
  const safe = name.trim() || 'my-pipeline'
  if (/^\s*name:/m.test(yaml)) {
    return yaml.replace(/^(\s*name:)\s*.*$/m, `$1 ${safe}`)
  }
  return yaml
}

const TYPE_OPTIONS: { type: ScheduleType; label: string; desc: string }[] = [
  { type: 'immediate', label: 'Immediate', desc: 'Trigger a run as soon as the schedule is created.' },
  { type: 'once',      label: 'Once',      desc: 'Run once at a specified time.' },
  { type: 'cron',      label: 'Cron',      desc: 'Run repeatedly on a cron schedule.' },
]

export default function WorkflowCreatePage() {
  const navigate = useNavigate()
  const [name, setName] = useState('my-pipeline')
  const [yaml, setYaml] = useState(EXAMPLE_YAML)
  const [scheduleType, setScheduleType] = useState<ScheduleType>('immediate')
  const [runAt, setRunAt] = useState('')
  const [cronExpr, setCronExpr] = useState('0 * * * *')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const runAtISO = useMemo(() => {
    if (!runAt) return ''
    const d = new Date(runAt)
    return Number.isNaN(d.getTime()) ? '' : d.toISOString()
  }, [runAt])

  async function handleSubmit() {
    setError('')
    const trimmedName = name.trim()
    const trimmedYaml = yaml.trim()

    if (!trimmedName) { setError('Pipeline name is required.'); return }
    if (!trimmedYaml) { setError('Pipeline YAML is required.'); return }
    if (scheduleType === 'once' && !runAtISO) { setError('Run time is required for once type.'); return }
    if (scheduleType === 'cron' && !cronExpr.trim()) { setError('Cron expression is required.'); return }

    try {
      setSubmitting(true)
      const normalizedYaml = applyPipelineName(trimmedYaml, trimmedName)
      const result = await createSchedule({
        name: trimmedName,
        yaml: normalizedYaml,
        type: scheduleType,
        cron: scheduleType === 'cron' ? cronExpr.trim() : undefined,
        run_at: scheduleType === 'once' ? runAtISO : undefined,
      })
      navigate(`/schedules/${result.schedule_id}`)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="space-y-6">
      <section className="rounded-xl border border-gray-800 bg-gray-900 p-6">
        <h1 className="text-lg font-semibold text-gray-100">Create Schedule</h1>
        <p className="mt-1 text-sm text-gray-400">Register a pipeline and choose how it should be triggered.</p>
      </section>

      <section className="rounded-xl border border-gray-800 bg-gray-950 p-6">
        <div className="space-y-5">
          <div>
            <label className="mb-2 block text-sm font-medium text-gray-300">Pipeline Name</label>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="w-full rounded-lg border border-gray-700 bg-gray-900 px-3 py-2 text-sm text-gray-100 focus:border-indigo-500 focus:outline-none"
              placeholder="my-pipeline"
            />
          </div>

          <div>
            <p className="mb-2 block text-sm font-medium text-gray-300">Trigger Type</p>
            <div className="grid gap-2 sm:grid-cols-3">
              {TYPE_OPTIONS.map(({ type, label, desc }) => (
                <button
                  key={type}
                  type="button"
                  onClick={() => setScheduleType(type)}
                  className={`rounded-lg border px-3 py-3 text-left text-sm transition-colors ${scheduleType === type ? 'border-indigo-500 bg-indigo-500/10 text-indigo-300' : 'border-gray-700 bg-gray-900 text-gray-300 hover:bg-gray-800'}`}
                >
                  <div className="font-semibold">{label}</div>
                  <div className="mt-0.5 text-xs opacity-70">{desc}</div>
                </button>
              ))}
            </div>
          </div>

          {scheduleType === 'once' && (
            <div>
              <label className="mb-2 block text-sm font-medium text-gray-300">Run At</label>
              <input
                type="datetime-local"
                value={runAt}
                onChange={(e) => setRunAt(e.target.value)}
                className="w-full rounded-lg border border-gray-700 bg-gray-900 px-3 py-2 text-sm text-gray-100 focus:border-indigo-500 focus:outline-none"
              />
            </div>
          )}

          {scheduleType === 'cron' && (
            <div>
              <label className="mb-2 block text-sm font-medium text-gray-300">Cron Expression</label>
              <input
                value={cronExpr}
                onChange={(e) => setCronExpr(e.target.value)}
                className="w-full rounded-lg border border-gray-700 bg-gray-900 px-3 py-2 text-sm text-gray-100 focus:border-indigo-500 focus:outline-none font-mono"
                placeholder="0 * * * *"
              />
              <p className="mt-1 text-xs text-gray-500">minute hour day month weekday — e.g. <code>*/15 * * * *</code> (every 15 min)</p>
            </div>
          )}

          <div>
            <label className="mb-2 block text-sm font-medium text-gray-300">Pipeline YAML</label>
            <textarea
              className="w-full resize-none rounded-lg border border-gray-700 bg-gray-900 p-3 font-mono text-sm text-gray-100 focus:border-indigo-500 focus:outline-none"
              rows={14}
              value={yaml}
              onChange={(e) => setYaml(e.target.value)}
              spellCheck={false}
            />
          </div>

          {error && <p className="text-sm text-red-400">{error}</p>}

          <div className="flex items-center justify-end gap-3">
            <button
              type="button"
              onClick={() => navigate('/schedules')}
              className="rounded-lg border border-gray-700 px-4 py-2 text-sm font-medium text-gray-300 hover:bg-gray-900"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={handleSubmit}
              disabled={submitting}
              className="rounded-lg bg-indigo-600 px-5 py-2 text-sm font-semibold text-white transition-colors hover:bg-indigo-500 disabled:opacity-50"
            >
              {submitting ? 'Submitting...' : 'Create Schedule'}
            </button>
          </div>
        </div>
      </section>
    </div>
  )
}
