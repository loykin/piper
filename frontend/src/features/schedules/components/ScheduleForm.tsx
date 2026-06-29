// schedules feature — Schedule creation form component
import { useMemo, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { YamlMirror } from '@/components/ui/yaml-mirror'
import { useCreateSchedule } from '../hooks'
import { parseMaxRuns } from '../maxRuns'

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

interface ScheduleFormProps {
  initialYaml?: string
  onCreated: (scheduleId: string) => void
  onCancel?: () => void
}

export function ScheduleForm({ initialYaml, onCreated, onCancel }: ScheduleFormProps) {
  const { mutateAsync: createSchedule, isPending: submitting } = useCreateSchedule()

  const [name, setName] = useState(() => {
    if (initialYaml) {
      const match = initialYaml.match(/^\s*name:\s*(.+)$/m)
      return match?.[1]?.trim() ?? 'my-pipeline'
    }
    return 'my-pipeline'
  })
  const [yaml, setYaml] = useState(initialYaml ?? EXAMPLE_YAML)
  const [scheduleType, setScheduleType] = useState<ScheduleType>('immediate')
  const [runAt, setRunAt] = useState('')
  const [cronExpr, setCronExpr] = useState('0 * * * *')
  const [maxRuns, setMaxRuns] = useState('')
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
    const parsedMaxRuns = parseMaxRuns(maxRuns)
    if (parsedMaxRuns == null) {
      setError('Max runs must be a non-negative integer.')
      return
    }

    try {
      const normalizedYaml = applyPipelineName(trimmedYaml, trimmedName)
      const result = await createSchedule({
        name: trimmedName,
        yaml: normalizedYaml,
        type: scheduleType,
        cron: scheduleType === 'cron' ? cronExpr.trim() : undefined,
        run_at: scheduleType === 'once' ? runAtISO : undefined,
        max_runs: parsedMaxRuns,
      })
      onCreated(result.schedule_id)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <div className="space-y-5 p-6">
      <div>
        <label className="mb-2 block text-sm font-medium">Pipeline Name</label>
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
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
          <Input
            type="datetime-local"
            value={runAt}
            onChange={(e) => setRunAt(e.target.value)}
          />
        </div>
      )}

      {scheduleType === 'cron' && (
        <div>
          <label className="mb-2 block text-sm font-medium">Cron Expression</label>
          <Input
            value={cronExpr}
            onChange={(e) => setCronExpr(e.target.value)}
            className="font-mono"
            placeholder="0 * * * *"
          />
          <p className="mt-1 text-xs text-muted-foreground">minute hour day month weekday — e.g. <code>*/15 * * * *</code></p>
        </div>
      )}

      <div>
        <label className="mb-2 block text-sm font-medium">Max Runs</label>
        <Input
          type="number"
          min={0}
          step={1}
          value={maxRuns}
          onChange={(e) => setMaxRuns(e.target.value)}
          placeholder="0"
        />
        <p className="mt-1 text-xs text-muted-foreground">0 keeps all completed runs.</p>
      </div>

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
        {onCancel && (
          <Button type="button" variant="outline" onClick={onCancel}>Cancel</Button>
        )}
        <Button
          type="button"
          onClick={() => void handleSubmit()}
          disabled={submitting}
        >
          {submitting ? 'Submitting...' : 'Create Schedule'}
        </Button>
      </div>
    </div>
  )
}
