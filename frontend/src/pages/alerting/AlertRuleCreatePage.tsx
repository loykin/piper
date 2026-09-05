import { useMemo, useState } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import { useNavigate } from '@tanstack/react-router'
import {
  DataBodyTemplate,
  FormActions,
  FormField,
  PageTopBar,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@loykin/designkit'
import { Controller, useForm, useWatch } from 'react-hook-form'
import { z } from 'zod'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Link } from '@/lib/router'
import { useCreateAlertRule } from '@/features/alerting/hooks'
import { useCredentials } from '@/features/credentials/hooks'
import { useProjectId } from '@/lib/projectContext'

const EVENT_TYPES = [
  'run.completed',
  'service.status',
  'notebook.running',
  'notebook.stopped',
  'notebook.failed',
] as const
type EventType = (typeof EVENT_TYPES)[number]

// "No filter" — an empty `when` matches every event of the selected type
// (pkg/alerting/eval.go's MatchEvent: `if rule.When == "" { return true }`).
// Never used as an actual field name, so it's safe as a Select sentinel.
const NO_FILTER = '__any__'

type FieldSpec =
  | { field: string; label: string; kind: 'enum'; options: { value: string; label: string }[] }
  | { field: string; label: string; kind: 'text'; placeholder: string }

// The field list per event type mirrors exactly what the backend actually
// populates into event.Event.Fields for each type — not a guess:
//   run.completed    -> {run_id, status}        (internal/queue/queue.go finalizeRunLocked)
//   service.status   -> {name, status}          (pkg/serving/status.go)
//   notebook.running/stopped/failed -> {name}   (pkg/notebook/status.go — status is implied by the event type itself)
// Status enums mirror each domain's actual terminal/status constants
// (pkg/pipeline/run, pkg/serving, pkg/notebook model.go).
const EVENT_FIELDS: Record<EventType, FieldSpec[]> = {
  'run.completed': [
    { field: 'status', label: 'Status', kind: 'enum', options: [
      { value: 'success', label: 'Success' },
      { value: 'failed', label: 'Failed' },
      { value: 'canceled', label: 'Canceled' },
    ] },
    { field: 'run_id', label: 'Run ID', kind: 'text', placeholder: 'run-abc123' },
  ],
  'service.status': [
    { field: 'status', label: 'Status', kind: 'enum', options: [
      { value: 'running', label: 'Running' },
      { value: 'stopped', label: 'Stopped' },
      { value: 'failed', label: 'Failed' },
    ] },
    { field: 'name', label: 'Service name', kind: 'text', placeholder: 'my-service' },
  ],
  'notebook.running': [{ field: 'name', label: 'Notebook name', kind: 'text', placeholder: 'my-notebook' }],
  'notebook.stopped': [{ field: 'name', label: 'Notebook name', kind: 'text', placeholder: 'my-notebook' }],
  'notebook.failed': [{ field: 'name', label: 'Notebook name', kind: 'text', placeholder: 'my-notebook' }],
}

// Mirrors eventConditionRE `(==|!=)` in pkg/alerting/eval.go.
const EVENT_OPERATORS = [
  { value: '==', label: 'is' },
  { value: '!=', label: 'is not' },
] as const

// Mirrors metricConditionRE `(<=|>=|<|>|==|!=)` in pkg/alerting/eval.go.
const METRIC_OPERATORS = [
  { value: '<', label: '<' },
  { value: '<=', label: '≤' },
  { value: '>', label: '>' },
  { value: '>=', label: '≥' },
  { value: '==', label: '=' },
  { value: '!=', label: '≠' },
] as const

// Mirrors metricConditionRE's number group exactly, so a value the UI
// accepts is guaranteed to pass the backend's validation too.
const METRIC_NUMBER_RE = /^-?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$/

function fieldsFor(eventType: string): FieldSpec[] {
  return EVENT_FIELDS[eventType as EventType] ?? []
}

const ruleSchema = z.object({
  name: z.string().trim().min(1, 'Name is required.'),
  source: z.enum(['event', 'metric']),
  eventType: z.string(),
  whenField: z.string(),
  whenOperator: z.enum(['==', '!=']),
  whenValue: z.string(),
  metricKey: z.string(),
  conditionOperator: z.enum(['<', '<=', '>', '>=', '==', '!=']),
  conditionValue: z.string(),
  cooldown: z.number().int().min(10, 'Cooldown must be at least 10 seconds.'),
  notify: z.array(z.string()).min(1, 'Select at least one notification channel.'),
}).superRefine((values, ctx) => {
  if (values.source === 'event' && values.whenField !== NO_FILTER) {
    const value = values.whenValue.trim()
    if (!value) {
      ctx.addIssue({ code: 'custom', path: ['whenValue'], message: 'Value is required.' })
    } else if (/["\\]/.test(value)) {
      ctx.addIssue({ code: 'custom', path: ['whenValue'], message: 'Value cannot contain " or \\.' })
    } else if (value.length > 256) {
      ctx.addIssue({ code: 'custom', path: ['whenValue'], message: 'Value must be at most 256 characters.' })
    }
  }
  if (values.source === 'metric') {
    if (!values.metricKey.trim()) {
      ctx.addIssue({ code: 'custom', path: ['metricKey'], message: 'Metric key is required.' })
    }
    if (!METRIC_NUMBER_RE.test(values.conditionValue.trim())) {
      ctx.addIssue({ code: 'custom', path: ['conditionValue'], message: 'Enter a number, e.g. 0.8' })
    }
  }
})

type RuleValues = z.infer<typeof ruleSchema>

export default function AlertRuleCreatePage() {
  const projectId = useProjectId()
  const navigate = useNavigate()
  const createRule = useCreateAlertRule()
  const credentials = useCredentials()
  const [submitError, setSubmitError] = useState('')
  const channels = useMemo(
    () => (credentials.data ?? []).filter(item => item.kind === 'slack' || item.kind === 'webhook'),
    [credentials.data],
  )
  const {
    control,
    register,
    handleSubmit,
    setValue,
    formState: { errors },
  } = useForm<RuleValues>({
    resolver: zodResolver(ruleSchema),
    defaultValues: {
      name: '',
      source: 'event',
      eventType: 'run.completed',
      whenField: 'status',
      whenOperator: '==',
      whenValue: 'failed',
      metricKey: '',
      conditionOperator: '<',
      conditionValue: '0.8',
      cooldown: 300,
      notify: [],
    },
  })
  const source = useWatch({ control, name: 'source' })
  const eventType = useWatch({ control, name: 'eventType' })
  const whenField = useWatch({ control, name: 'whenField' })
  const selectedChannels = useWatch({ control, name: 'notify' })
  const listPath = `/projects/${projectId}/alert-rules`
  const currentFields = fieldsFor(eventType)
  const currentFieldSpec = currentFields.find(f => f.field === whenField)

  function handleEventTypeChange(next: string) {
    setValue('eventType', next)
    const fields = fieldsFor(next)
    const stillValid = fields.some(f => f.field === whenField)
    if (!stillValid) {
      const first = fields[0]
      setValue('whenField', first ? first.field : NO_FILTER)
      setValue('whenOperator', '==')
      setValue('whenValue', first?.kind === 'enum' ? first.options[0].value : '')
    }
  }

  function handleWhenFieldChange(next: string) {
    setValue('whenField', next)
    if (next === NO_FILTER) {
      setValue('whenValue', '')
      return
    }
    const spec = currentFields.find(f => f.field === next)
    setValue('whenValue', spec?.kind === 'enum' ? spec.options[0].value : '')
  }

  async function submit(values: RuleValues) {
    setSubmitError('')
    try {
      const when = values.source === 'event' && values.whenField !== NO_FILTER
        ? `fields.${values.whenField} ${values.whenOperator} "${values.whenValue.trim()}"`
        : ''
      const condition = values.source === 'metric'
        ? `${values.conditionOperator} ${values.conditionValue.trim()}`
        : ''
      await createRule.mutateAsync({
        name: values.name,
        on: values.source,
        event_type: values.source === 'event' ? values.eventType : undefined,
        when: values.source === 'event' ? when : undefined,
        metric_key: values.source === 'metric' ? values.metricKey.trim() : undefined,
        condition: values.source === 'metric' ? condition : undefined,
        notify: values.notify,
        cooldown_seconds: values.cooldown,
        enabled: true,
      })
      void navigate({ to: listPath })
    } catch (cause) {
      setSubmitError(cause instanceof Error ? cause.message : String(cause))
    }
  }

  function toggleChannel(name: string, checked: boolean) {
    const next = checked
      ? [...selectedChannels, name]
      : selectedChannels.filter(value => value !== name)
    setValue('notify', next, { shouldDirty: true, shouldValidate: true })
  }

  return (
    <DataBodyTemplate
      topBar={<PageTopBar left="Alert Rules / New Rule" />}
      title="New Alert Rule"
      description="Notify one or more project channels when an event or metric condition matches."
    >
      <DataBodyTemplate.Group
        layout="stacked"
        title="Rule"
        description="Rules are evaluated by the Member that owns this project."
      >
        <form className="space-y-3" noValidate onSubmit={handleSubmit(submit)}>
          <FormField label="Name" htmlFor="alert-name" error={errors.name?.message}>
            <Input id="alert-name" placeholder="run-failures" aria-invalid={!!errors.name} {...register('name')} />
          </FormField>
          <FormField label="Source" htmlFor="alert-source">
            <Controller
              name="source"
              control={control}
              render={({ field }) => (
                <Select
                  items={[{ value: 'event', label: 'Event' }, { value: 'metric', label: 'Metric' }]}
                  value={field.value || null}
                  onValueChange={value => field.onChange(value ?? 'event')}
                >
                  <SelectTrigger id="alert-source" className="w-40"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="event">Event</SelectItem>
                    <SelectItem value="metric">Metric</SelectItem>
                  </SelectContent>
                </Select>
              )}
            />
          </FormField>
          {source === 'event' ? (
            <>
              <FormField label="Event type" htmlFor="event-type" error={errors.eventType?.message}>
                <Select
                  items={EVENT_TYPES.map(value => ({ value, label: value }))}
                  value={eventType || null}
                  onValueChange={value => handleEventTypeChange(value ?? EVENT_TYPES[0])}
                >
                  <SelectTrigger id="event-type" className="w-64"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {EVENT_TYPES.map(value => <SelectItem key={value} value={value}>{value}</SelectItem>)}
                  </SelectContent>
                </Select>
              </FormField>
              <FormField label="When" htmlFor="when-field" helperText="Leave as “Any” to alert on every event of this type.">
                <div className="flex items-center gap-2">
                  <Select
                    items={[{ value: NO_FILTER, label: 'Any' }, ...currentFields.map(f => ({ value: f.field, label: f.label }))]}
                    value={whenField || null}
                    onValueChange={value => handleWhenFieldChange(value ?? NO_FILTER)}
                  >
                    <SelectTrigger id="when-field" className="w-44"><SelectValue /></SelectTrigger>
                    <SelectContent>
                      <SelectItem value={NO_FILTER}>Any</SelectItem>
                      {currentFields.map(f => <SelectItem key={f.field} value={f.field}>{f.label}</SelectItem>)}
                    </SelectContent>
                  </Select>
                  {whenField !== NO_FILTER && (
                    <>
                      <Controller
                        name="whenOperator"
                        control={control}
                        render={({ field }) => (
                          <Select
                            items={EVENT_OPERATORS.map(op => ({ value: op.value, label: op.label }))}
                            value={field.value || null}
                            onValueChange={value => field.onChange(value ?? '==')}
                          >
                            <SelectTrigger className="w-28"><SelectValue /></SelectTrigger>
                            <SelectContent>
                              {EVENT_OPERATORS.map(op => <SelectItem key={op.value} value={op.value}>{op.label}</SelectItem>)}
                            </SelectContent>
                          </Select>
                        )}
                      />
                      {currentFieldSpec?.kind === 'enum' ? (
                        <Controller
                          name="whenValue"
                          control={control}
                          render={({ field }) => (
                            <Select
                              items={currentFieldSpec.options}
                              value={field.value || null}
                              onValueChange={value => field.onChange(value ?? '')}
                            >
                              <SelectTrigger className="w-40"><SelectValue /></SelectTrigger>
                              <SelectContent>
                                {currentFieldSpec.options.map(opt => <SelectItem key={opt.value} value={opt.value}>{opt.label}</SelectItem>)}
                              </SelectContent>
                            </Select>
                          )}
                        />
                      ) : (
                        <Input
                          className="w-40 font-mono"
                          placeholder={currentFieldSpec?.kind === 'text' ? currentFieldSpec.placeholder : ''}
                          aria-invalid={!!errors.whenValue}
                          {...register('whenValue')}
                        />
                      )}
                    </>
                  )}
                </div>
                {errors.whenValue?.message && <p className="mt-1 text-sm text-destructive">{errors.whenValue.message}</p>}
              </FormField>
            </>
          ) : (
            <>
              <FormField label="Metric key" htmlFor="metric-key" error={errors.metricKey?.message}>
                <Input id="metric-key" className="font-mono" placeholder="accuracy" aria-invalid={!!errors.metricKey} {...register('metricKey')} />
              </FormField>
              <FormField label="Condition" htmlFor="metric-condition-value" error={errors.conditionValue?.message}>
                <div className="flex items-center gap-2">
                  <Controller
                    name="conditionOperator"
                    control={control}
                    render={({ field }) => (
                      <Select
                        items={METRIC_OPERATORS.map(op => ({ value: op.value, label: op.label }))}
                        value={field.value || null}
                        onValueChange={value => field.onChange(value ?? '<')}
                      >
                        <SelectTrigger className="w-24"><SelectValue /></SelectTrigger>
                        <SelectContent>
                          {METRIC_OPERATORS.map(op => <SelectItem key={op.value} value={op.value}>{op.label}</SelectItem>)}
                        </SelectContent>
                      </Select>
                    )}
                  />
                  <Input
                    id="metric-condition-value"
                    type="number"
                    step="any"
                    className="w-32 font-mono"
                    placeholder="0.8"
                    aria-invalid={!!errors.conditionValue}
                    {...register('conditionValue')}
                  />
                </div>
              </FormField>
            </>
          )}
          <FormField label="Cooldown (seconds)" htmlFor="alert-cooldown" error={errors.cooldown?.message}>
            <Input id="alert-cooldown" type="number" min={10} aria-invalid={!!errors.cooldown} {...register('cooldown', { valueAsNumber: true })} />
          </FormField>
          <div className="space-y-2">
            <Label>Notification channels</Label>
            {channels.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                No Slack or webhook credential yet.{' '}
                <Link to={`/projects/${projectId}/credentials/new`} search={{ kind: 'slack' }} className="underline underline-offset-2 hover:text-foreground">
                  Create one
                </Link>.
              </p>
            ) : channels.map(channel => (
              <div key={channel.name} className="flex items-center justify-between rounded-md border p-3">
                <div>
                  <p className="text-sm font-medium">{channel.name}</p>
                  <p className="text-xs text-muted-foreground">{channel.kind}</p>
                </div>
                <Switch
                  aria-label={`Send notifications to ${channel.name}`}
                  checked={selectedChannels.includes(channel.name)}
                  onCheckedChange={checked => toggleChannel(channel.name, checked)}
                />
              </div>
            ))}
            {errors.notify?.message && <p className="text-sm text-destructive">{errors.notify.message}</p>}
          </div>
          <FormActions
            status={submitError || undefined}
            submitLabel={createRule.isPending ? 'Creating…' : 'Create Rule'}
            submitDisabled={createRule.isPending || channels.length === 0}
            onCancel={() => void navigate({ to: listPath })}
          />
        </form>
      </DataBodyTemplate.Group>
    </DataBodyTemplate>
  )
}
