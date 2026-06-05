import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { DataPage } from '@loykin/designkit'
import { YamlMirror } from '@/components/ui/yaml-mirror'
import { createSchedule } from '@/features/schedules/api'

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
    return yaml.replace(/^(\s*name:)\s*.*$/m, (_, prefix) => `${prefix} ${JSON.stringify(safe)}`)
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

  useEffect(() => {
    const draft = sessionStorage.getItem('piper.pipeline.editor.draft')
    if (!draft) return
    setYaml(draft)
    const nameMatch = draft.match(/^\s*name:\s*(.+)$/m)
    if (nameMatch?.[1]) {
      setName(nameMatch[1].trim())
    }
  }, [])

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

  const inputCls = 'w-full rounded-lg border border-border bg-card px-3 py-2 text-sm text-foreground focus:border-primary focus:outline-none'

  return (
    <DataPage>
      <DataPage.Header>
        <DataPage.TitleBlock
          title="Create Schedule"
          description="Register a pipeline and choose how it should be triggered."
        />
      </DataPage.Header>

      <DataPage.Content>
        <DataPage.Group surface="bordered" className="max-w-2xl">
          <div className="space-y-5 p-6">
            <div>
              <label className="mb-2 block text-sm font-medium">Pipeline Name</label>
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                className={inputCls}
                placeholder="my-pipeline"
              />
            </div>

            <div>
              <p className="mb-2 block text-sm font-medium">Trigger Type</p>
              <div className="grid gap-2 sm:grid-cols-3">
                {TYPE_OPTIONS.map(({ type, label, desc }) => (
                  <button
                    key={type}
                    type="button"
                    onClick={() => setScheduleType(type)}
                    className={`rounded-lg border px-3 py-3 text-left text-sm transition-colors ${
                      scheduleType === type
                        ? 'border-primary bg-primary/10 text-primary'
                        : 'border-border bg-card text-foreground hover:bg-accent'
                    }`}
                  >
                    <div className="font-semibold">{label}</div>
                    <div className="mt-0.5 text-xs opacity-70">{desc}</div>
                  </button>
                ))}
              </div>
            </div>

            {scheduleType === 'once' && (
              <div>
                <label className="mb-2 block text-sm font-medium">Run At</label>
                <input
                  type="datetime-local"
                  value={runAt}
                  onChange={(e) => setRunAt(e.target.value)}
                  className={inputCls}
                />
              </div>
            )}

            {scheduleType === 'cron' && (
              <div>
                <label className="mb-2 block text-sm font-medium">Cron Expression</label>
                <input
                  value={cronExpr}
                  onChange={(e) => setCronExpr(e.target.value)}
                  className={`${inputCls} font-mono`}
                  placeholder="0 * * * *"
                />
                <p className="mt-1 text-xs text-muted-foreground">minute hour day month weekday — e.g. <code>*/15 * * * *</code></p>
              </div>
            )}

            <div>
              <label className="mb-2 block text-sm font-medium">Pipeline YAML</label>
              <YamlMirror
                className="bg-background"
                rows={14}
                value={yaml}
                onChange={(e) => setYaml(e.target.value)}
              />
            </div>

            {error && <p className="text-sm text-destructive">{error}</p>}

            <div className="flex items-center justify-end gap-3">
              <button
                type="button"
                onClick={() => navigate('/schedules')}
                className="rounded-lg border border-border px-4 py-2 text-sm font-medium hover:bg-accent"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={handleSubmit}
                disabled={submitting}
                className="rounded-lg bg-primary px-5 py-2 text-sm font-semibold text-primary-foreground hover:opacity-90 disabled:opacity-50"
              >
                {submitting ? 'Submitting...' : 'Create Schedule'}
              </button>
            </div>
          </div>
        </DataPage.Group>
      </DataPage.Content>
    </DataPage>
  )
}
