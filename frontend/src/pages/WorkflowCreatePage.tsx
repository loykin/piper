import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { createRun, createSchedule } from '../api'

type ExecutionMode = 'now' | 'once' | 'cron'

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

export default function WorkflowCreatePage() {
  const navigate = useNavigate()
  const [name, setName] = useState('my-pipeline')
  const [yaml, setYaml] = useState(EXAMPLE_YAML)
  const [executionMode, setExecutionMode] = useState<ExecutionMode>('now')
  const [scheduleAt, setScheduleAt] = useState('')
  const [cronExpr, setCronExpr] = useState('0 * * * *')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const scheduleAtISO = useMemo(() => {
    if (!scheduleAt) return ''
    const d = new Date(scheduleAt)
    if (Number.isNaN(d.getTime())) return ''
    return d.toISOString()
  }, [scheduleAt])

  async function handleCreateAndRun() {
    setError('')

    const trimmedName = name.trim()
    const trimmedYaml = yaml.trim()
    if (!trimmedName) {
      setError('Pipeline name is required.')
      return
    }
    if (!trimmedYaml) {
      setError('Pipeline YAML is required.')
      return
    }

    if (executionMode === 'once' && !scheduleAtISO) {
      setError('One-time execution requires a valid schedule time.')
      return
    }
    if (executionMode === 'cron' && !cronExpr.trim()) {
      setError('Cron expression is required for cron schedule.')
      return
    }

    try {
      setSubmitting(true)
      const normalizedYaml = applyPipelineName(trimmedYaml, trimmedName)

      if (executionMode === 'cron') {
        await createSchedule({
          name: trimmedName,
          yaml: normalizedYaml,
          cron: cronExpr.trim(),
        })
        navigate('/pipelines')
        return
      }

      const result = await createRun(normalizedYaml, {
        vars: executionMode === 'once' ? { scheduled_at: scheduleAtISO } : undefined,
      })
      navigate(`/runs/${result.run_id}`)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="space-y-6">
      <section className="rounded-xl border border-gray-800 bg-gray-900 p-6">
        <h1 className="text-lg font-semibold text-gray-100">Create Pipeline</h1>
        <p className="mt-1 text-sm text-gray-400">Choose execution mode and submit a pipeline run.</p>
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
            <p className="mb-2 block text-sm font-medium text-gray-300">Execution Mode</p>
            <div className="grid gap-2 sm:grid-cols-3">
              <button
                type="button"
                onClick={() => setExecutionMode('now')}
                className={`rounded-lg border px-3 py-2 text-left text-sm ${executionMode === 'now' ? 'border-indigo-500 bg-indigo-500/10 text-indigo-300' : 'border-gray-700 bg-gray-900 text-gray-300 hover:bg-gray-800'}`}
              >
                Run Now
              </button>
              <button
                type="button"
                onClick={() => setExecutionMode('once')}
                className={`rounded-lg border px-3 py-2 text-left text-sm ${executionMode === 'once' ? 'border-indigo-500 bg-indigo-500/10 text-indigo-300' : 'border-gray-700 bg-gray-900 text-gray-300 hover:bg-gray-800'}`}
              >
                Run Once (Scheduled)
              </button>
              <button
                type="button"
                onClick={() => setExecutionMode('cron')}
                className={`rounded-lg border px-3 py-2 text-left text-sm ${executionMode === 'cron' ? 'border-indigo-500 bg-indigo-500/10 text-indigo-300' : 'border-gray-700 bg-gray-900 text-gray-300 hover:bg-gray-800'}`}
              >
                Cron Schedule
              </button>
            </div>
          </div>

          {executionMode === 'once' && (
            <div>
              <label className="mb-2 block text-sm font-medium text-gray-300">Schedule At</label>
              <input
                type="datetime-local"
                value={scheduleAt}
                onChange={(e) => setScheduleAt(e.target.value)}
                className="w-full rounded-lg border border-gray-700 bg-gray-900 px-3 py-2 text-sm text-gray-100 focus:border-indigo-500 focus:outline-none"
              />
              <p className="mt-2 text-xs text-gray-500">
                Note: this stores logical scheduled time (`PIPER_SCHEDULED_AT`) and executes immediately with current backend.
              </p>
            </div>
          )}

          {executionMode === 'cron' && (
            <div>
              <label className="mb-2 block text-sm font-medium text-gray-300">Cron Expression</label>
              <input
                value={cronExpr}
                onChange={(e) => setCronExpr(e.target.value)}
                className="w-full rounded-lg border border-gray-700 bg-gray-900 px-3 py-2 text-sm text-gray-100 focus:border-indigo-500 focus:outline-none"
                placeholder="0 * * * *"
              />
              <p className="mt-2 text-xs text-gray-500">Format: minute hour day month weekday (e.g. `*/15 * * * *`).</p>
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
              onClick={() => navigate('/pipelines')}
              className="rounded-lg border border-gray-700 px-4 py-2 text-sm font-medium text-gray-300 hover:bg-gray-900"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={handleCreateAndRun}
              disabled={submitting}
              className="rounded-lg bg-indigo-600 px-5 py-2 text-sm font-semibold text-white transition-colors hover:bg-indigo-500 disabled:opacity-50"
            >
              {submitting ? 'Submitting...' : executionMode === 'cron' ? 'Create Schedule' : 'Create & Run'}
            </button>
          </div>
        </div>
      </section>
    </div>
  )
}
