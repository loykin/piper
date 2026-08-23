// schedules feature — Schedule creation form component
import { useMemo, useState } from 'react'
import { CronInput, toCronExpression, validateCronExpression, type CronValue } from '@loykin/cron-input'
import { createShadcnAdapter } from '@loykin/cron-input/adapters/shadcn'
import { FormActions, FormField } from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Popover, PopoverTrigger, PopoverContent } from '@/components/ui/popover'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { YamlMirror } from '@/components/ui/yaml-mirror'
import { useCreateSchedule } from '../hooks'
import { parseMaxRuns } from '../maxRuns'

// Built once at module scope — uiAdapter must be referentially stable, or the
// adapted subtree remounts on every render (see @loykin/cron-input README).
const cronInputShadcnAdapter = createShadcnAdapter({
  Button, Popover, PopoverTrigger, PopoverContent,
  Tabs, TabsList, TabsTrigger, TabsContent,
})

const DEFAULT_CRON_VALUE: CronValue = { type: 'interval', every: 1, unit: 'hour' }

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
  const [cronValue, setCronValue] = useState<CronValue>(DEFAULT_CRON_VALUE)
  const [maxRuns, setMaxRuns] = useState('')
  const [error, setError] = useState('')

  const runAtISO = useMemo(() => {
    if (!runAt) return ''
    const d = new Date(runAt)
    return Number.isNaN(d.getTime()) ? '' : d.toISOString()
  }, [runAt])

  const cronExpr = useMemo(() => toCronExpression(cronValue), [cronValue])
  const cronValid = cronValue.type !== 'custom' || validateCronExpression(cronValue.expression)

  async function handleSubmit() {
    setError('')
    const trimmedName = name.trim()
    const trimmedYaml = yaml.trim()

    if (!trimmedName) { setError('Pipeline name is required.'); return }
    if (!trimmedYaml) { setError('Pipeline YAML is required.'); return }
    if (scheduleType === 'once' && !runAtISO) { setError('Run time is required for once type.'); return }
    if (scheduleType === 'cron' && !cronValid) { setError('Cron expression is invalid.'); return }
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
        cron: scheduleType === 'cron' ? cronExpr : undefined,
        run_at: scheduleType === 'once' ? runAtISO : undefined,
        max_runs: parsedMaxRuns,
      })
      onCreated(result.schedule_id)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <form
      className="space-y-3"
      onSubmit={(e) => {
        e.preventDefault()
        void handleSubmit()
      }}
    >
      <FormField label="Pipeline Name" htmlFor="schedule-pipeline-name">
        <Input
          id="schedule-pipeline-name"
          className="h-8 text-sm"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="my-pipeline"
        />
      </FormField>

      <div className="space-y-1.5">
        <Label className="text-xs">Trigger Type</Label>
        <div className="grid gap-2 sm:grid-cols-3">
          {TYPE_OPTIONS.map(({ type, label, desc }) => (
            <Button
              key={type}
              type="button"
              variant={scheduleType === type ? 'default' : 'outline'}
              onClick={() => setScheduleType(type)}
              className="h-auto flex-col items-start gap-0 py-3 text-left"
            >
              <div className="font-semibold">{label}</div>
              <div className="mt-0.5 text-xs opacity-70">{desc}</div>
            </Button>
          ))}
        </div>
      </div>

      {scheduleType === 'once' && (
        <FormField label="Run At" htmlFor="schedule-run-at">
          <Input
            id="schedule-run-at"
            type="datetime-local"
            className="h-8 text-sm"
            value={runAt}
            onChange={(e) => setRunAt(e.target.value)}
          />
        </FormField>
      )}

      {scheduleType === 'cron' && (
        <FormField
          label="Cron Schedule"
          htmlFor="schedule-cron"
          error={!cronValid ? 'Cron expression is invalid.' : undefined}
        >
          <CronInput
            value={cronValue}
            onChange={setCronValue}
            uiAdapter={cronInputShadcnAdapter}
          />
        </FormField>
      )}

      <FormField label="Retention" htmlFor="schedule-max-runs" helperText="Completed run records to keep, not a limit on how many times this schedule fires. 0 keeps all of them.">
        <Input
          id="schedule-max-runs"
          type="number"
          min={0}
          step={1}
          className="h-8 text-sm"
          value={maxRuns}
          onChange={(e) => setMaxRuns(e.target.value)}
          placeholder="0"
        />
      </FormField>

      <FormField label="Pipeline YAML" htmlFor="schedule-yaml">
        <YamlMirror
          className="bg-background"
          rows={14}
          value={yaml}
          onChange={(e) => setYaml(e.target.value)}
        />
      </FormField>

      <FormActions
        status={error || undefined}
        submitLabel={submitting ? 'Submitting…' : 'Create Schedule'}
        submitDisabled={submitting}
        onCancel={onCancel}
      />
    </form>
  )
}
