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

const ruleSchema = z.object({
  name: z.string().trim().min(1, 'Name is required.'),
  source: z.enum(['event', 'metric']),
  eventType: z.string(),
  when: z.string(),
  metricKey: z.string(),
  condition: z.string(),
  cooldown: z.number().int().min(10, 'Cooldown must be at least 10 seconds.'),
  notify: z.array(z.string()).min(1, 'Select at least one notification channel.'),
}).superRefine((values, ctx) => {
  if (values.source === 'event' && !values.eventType) {
    ctx.addIssue({ code: 'custom', path: ['eventType'], message: 'Event type is required.' })
  }
  if (values.source === 'metric' && !values.metricKey.trim()) {
    ctx.addIssue({ code: 'custom', path: ['metricKey'], message: 'Metric key is required.' })
  }
  if (values.source === 'metric' && !values.condition.trim()) {
    ctx.addIssue({ code: 'custom', path: ['condition'], message: 'Condition is required.' })
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
      when: 'fields.status == "failed"',
      metricKey: '',
      condition: '< 0.8',
      cooldown: 300,
      notify: [],
    },
  })
  const source = useWatch({ control, name: 'source' })
  const selectedChannels = useWatch({ control, name: 'notify' })
  const listPath = `/projects/${projectId}/alert-rules`

  async function submit(values: RuleValues) {
    setSubmitError('')
    try {
      await createRule.mutateAsync({
        name: values.name,
        on: values.source,
        event_type: values.source === 'event' ? values.eventType : undefined,
        when: values.source === 'event' ? values.when.trim() : undefined,
        metric_key: values.source === 'metric' ? values.metricKey.trim() : undefined,
        condition: values.source === 'metric' ? values.condition.trim() : undefined,
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
                <Select value={field.value} onValueChange={field.onChange}>
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
                <Controller
                  name="eventType"
                  control={control}
                  render={({ field }) => (
                    <Select value={field.value} onValueChange={field.onChange}>
                      <SelectTrigger id="event-type" className="w-64"><SelectValue /></SelectTrigger>
                      <SelectContent>
                        {EVENT_TYPES.map(value => <SelectItem key={value} value={value}>{value}</SelectItem>)}
                      </SelectContent>
                    </Select>
                  )}
                />
              </FormField>
              <FormField label="When" htmlFor="event-when" error={errors.when?.message} helperText={'Example: fields.status == "failed"'}>
                <Input id="event-when" className="font-mono" {...register('when')} />
              </FormField>
            </>
          ) : (
            <>
              <FormField label="Metric key" htmlFor="metric-key" error={errors.metricKey?.message}>
                <Input id="metric-key" className="font-mono" placeholder="accuracy" aria-invalid={!!errors.metricKey} {...register('metricKey')} />
              </FormField>
              <FormField label="Condition" htmlFor="metric-condition" error={errors.condition?.message} helperText="Example: &lt; 0.8">
                <Input id="metric-condition" className="font-mono" aria-invalid={!!errors.condition} {...register('condition')} />
              </FormField>
            </>
          )}
          <FormField label="Cooldown (seconds)" htmlFor="alert-cooldown" error={errors.cooldown?.message}>
            <Input id="alert-cooldown" type="number" min={10} aria-invalid={!!errors.cooldown} {...register('cooldown', { valueAsNumber: true })} />
          </FormField>
          <div className="space-y-2">
            <Label>Notification channels</Label>
            {channels.length === 0 ? (
              <p className="text-sm text-muted-foreground">Create a Slack or webhook credential first.</p>
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
