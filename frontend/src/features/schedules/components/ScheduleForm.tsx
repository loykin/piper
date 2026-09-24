// schedules feature — Schedule creation form component
import { useState } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import { Controller, useForm, useWatch } from 'react-hook-form'
import { z } from 'zod'
import { CronInput, toCronExpression, validateCronExpression, type CronValue } from '@loykin/cron-input'
import { createShadcnAdapter } from '@loykin/cron-input/adapters/shadcn'
import { FormField } from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Popover, PopoverTrigger, PopoverContent } from '@/components/ui/popover'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { YamlMirror } from '@/components/ui/yaml-mirror'
import { useCreateSchedule } from '../hooks'
import { errorMessage } from '@/lib/format'
import { maxRunsField, toMaxRuns } from '../maxRuns'
import { FormSubmitBar } from '@/shared/components/FormSubmitBar'

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

const scheduleSchema = z.object({
  name: z.string().trim().min(1, 'Pipeline name is required.'),
  yaml: z.string().trim().min(1, 'Pipeline YAML is required.'),
  type: z.enum(['immediate', 'once', 'cron']),
  runAt: z.string(),
  cron: z.custom<CronValue>(),
  maxRuns: maxRunsField,
}).superRefine((values, ctx) => {
  if (values.type === 'once' && !toISO(values.runAt)) {
    ctx.addIssue({ code: 'custom', path: ['runAt'], message: 'Run time is required for a one-time schedule.' })
  }
  if (values.type === 'cron' && values.cron.type === 'custom' && !validateCronExpression(values.cron.expression)) {
    ctx.addIssue({ code: 'custom', path: ['cron'], message: 'Cron expression is invalid.' })
  }
})

type ScheduleValues = z.infer<typeof scheduleSchema>

function toISO(local: string): string {
  if (!local) return ''
  const d = new Date(local)
  return Number.isNaN(d.getTime()) ? '' : d.toISOString()
}

interface ScheduleFormProps {
  initialYaml?: string
  onCreated: (scheduleId: string) => void
  onCancel?: () => void
}

export function ScheduleForm({ initialYaml, onCreated, onCancel }: ScheduleFormProps) {
  const { mutateAsync: createSchedule, isPending: submitting } = useCreateSchedule()
  const [submitError, setSubmitError] = useState('')
  const { control, register, handleSubmit, formState: { errors } } = useForm<ScheduleValues>({
    resolver: zodResolver(scheduleSchema),
    defaultValues: {
      name: initialYaml?.match(/^\s*name:\s*(.+)$/m)?.[1]?.trim() ?? 'my-pipeline',
      yaml: initialYaml ?? EXAMPLE_YAML,
      type: 'immediate',
      runAt: '',
      cron: DEFAULT_CRON_VALUE,
      maxRuns: '',
    },
  })
  const scheduleType = useWatch({ control, name: 'type' })

  async function submit(values: ScheduleValues) {
    setSubmitError('')
    try {
      const result = await createSchedule({
        name: values.name,
        yaml: applyPipelineName(values.yaml, values.name),
        type: values.type,
        cron: values.type === 'cron' ? toCronExpression(values.cron) : undefined,
        run_at: values.type === 'once' ? toISO(values.runAt) : undefined,
        max_runs: toMaxRuns(values.maxRuns),
      })
      onCreated(result.schedule_id)
    } catch (cause) {
      setSubmitError(errorMessage(cause))
    }
  }

  return (
    <form className="space-y-3" noValidate onSubmit={handleSubmit(submit)}>
      <FormField label="Pipeline Name" htmlFor="schedule-pipeline-name" error={errors.name?.message} helperText="Defaults to my-pipeline.">
        <Input id="schedule-pipeline-name" className="h-8 text-sm" aria-invalid={!!errors.name} {...register('name')} />
      </FormField>

      <Controller
        name="type"
        control={control}
        render={({ field }) => (
          <div className="space-y-1.5">
            <Label className="text-xs">Trigger Type</Label>
            <div className="grid gap-2 sm:grid-cols-3">
              {TYPE_OPTIONS.map(({ type, label, desc }) => (
                <Button
                  key={type}
                  type="button"
                  variant={field.value === type ? 'default' : 'outline'}
                  onClick={() => field.onChange(type)}
                  className="h-auto flex-col items-start gap-0 py-3 text-left"
                >
                  <div className="font-semibold">{label}</div>
                  <div className="mt-0.5 text-xs opacity-70">{desc}</div>
                </Button>
              ))}
            </div>
          </div>
        )}
      />

      {scheduleType === 'once' && (
        <FormField label="Run At" htmlFor="schedule-run-at" error={errors.runAt?.message}>
          <Input id="schedule-run-at" type="datetime-local" className="h-8 text-sm" aria-invalid={!!errors.runAt} {...register('runAt')} />
        </FormField>
      )}

      {scheduleType === 'cron' && (
        <FormField label="Cron Schedule" htmlFor="schedule-cron" error={errors.cron?.message}>
          <Controller
            name="cron"
            control={control}
            render={({ field }) => (
              <CronInput value={field.value} onChange={field.onChange} uiAdapter={cronInputShadcnAdapter} />
            )}
          />
        </FormField>
      )}

      <FormField
        label="Retention"
        htmlFor="schedule-max-runs"
        error={errors.maxRuns?.message}
        helperText="Completed run records to keep, not a limit on how many times this schedule fires. Leave blank or 0 to keep all of them."
      >
        <Input id="schedule-max-runs" type="number" min={0} step={1} className="h-8 text-sm" aria-invalid={!!errors.maxRuns} {...register('maxRuns')} />
      </FormField>

      <FormField label="Pipeline YAML" htmlFor="schedule-yaml" error={errors.yaml?.message}>
        <Controller
          name="yaml"
          control={control}
          render={({ field }) => (
            <YamlMirror className="bg-background" rows={14} value={field.value} onChange={e => field.onChange(e.target.value)} />
          )}
        />
      </FormField>

      <FormSubmitBar
        verb="Create"
        noun="Schedule"
        pending={submitting}
        error={submitError}
        onCancel={onCancel}
      />
    </form>
  )
}
