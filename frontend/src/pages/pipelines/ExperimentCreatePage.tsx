import { useEffect, useMemo, useState } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import { useNavigate } from '@tanstack/react-router'
import { Controller, useForm } from 'react-hook-form'
import { z } from 'zod'
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
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { usePipelines } from '@/features/pipelines/hooks'
import { useCreateSweep } from '@/features/runs/hooks'
import { useProjectId } from '@/lib/projectContext'

const schema = z.object({
  experiment: z.string().trim().min(1, 'Experiment name is required.'),
  pipelineId: z.string().min(1, 'Select a pipeline.'),
  trials: z.string().superRefine((value, ctx) => {
    try {
      const parsed = JSON.parse(value) as unknown
      if (!Array.isArray(parsed) || parsed.length === 0 || parsed.some(item => typeof item !== 'object' || item == null || Array.isArray(item))) {
        ctx.addIssue({ code: 'custom', message: 'Enter a non-empty JSON array of parameter objects.' })
      }
    } catch {
      ctx.addIssue({ code: 'custom', message: 'Enter valid JSON.' })
    }
  }),
})

type Values = z.infer<typeof schema>

export default function ExperimentCreatePage() {
  const projectId = useProjectId()
  const navigate = useNavigate()
  const pipelinesQuery = usePipelines()
  const createSweep = useCreateSweep()
  const [submitError, setSubmitError] = useState('')
  const pipelines = useMemo(() => pipelinesQuery.data ?? [], [pipelinesQuery.data])
  const items = useMemo(() => pipelines.map(pipeline => ({ value: pipeline.id, label: `${pipeline.name} v${pipeline.version}` })), [pipelines])
  const listPath = `/projects/${projectId}/experiments`
  const { control, register, handleSubmit, setValue, formState: { errors } } = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { experiment: '', pipelineId: '', trials: '[\n  {"learning_rate": 0.01},\n  {"learning_rate": 0.1}\n]' },
  })
  // @base-ui/react's Select never calls onValueChange when it has exactly
  // one item (confirmed with pure keyboard input too, so it isn't an
  // automation-click artifact — docs/qa/adversarial-qa-playbook.md §3c): the
  // trigger visually shows the sole pipeline selected, but the field this
  // form submits stays empty. A project with exactly one pipeline is a
  // completely ordinary state, not an edge case.
  const solePipeline = pipelines.length === 1 ? pipelines[0].id : null
  useEffect(() => {
    if (solePipeline) setValue('pipelineId', solePipeline, { shouldValidate: true })
  }, [solePipeline, setValue])

  async function submit(values: Values) {
    setSubmitError('')
    const pipeline = pipelines.find(item => item.id === values.pipelineId)
    if (!pipeline) {
      setSubmitError('The selected pipeline is no longer available.')
      return
    }
    try {
      const trials = JSON.parse(values.trials) as Record<string, unknown>[]
      await createSweep.mutateAsync({
        yaml: pipeline.yaml,
        experiment: values.experiment.trim(),
        runs: trials.map(params => ({ params })),
      })
      void navigate({ to: listPath })
    } catch (cause) {
      setSubmitError(cause instanceof Error ? cause.message : String(cause))
    }
  }

  return (
    <DataBodyTemplate
      topBar={<PageTopBar left="Experiments / New Sweep" />}
      title="New Sweep"
      description="Run one saved pipeline with multiple parameter sets under a shared experiment name."
    >
      <DataBodyTemplate.Group title="Sweep">
        <form className="max-w-2xl space-y-6" onSubmit={event => void handleSubmit(submit)(event)}>
          <FormField label="Experiment name" htmlFor="experiment-name" error={errors.experiment?.message}>
            <Input id="experiment-name" placeholder="learning-rate-search" aria-invalid={!!errors.experiment} {...register('experiment')} />
          </FormField>
          <FormField label="Pipeline" htmlFor="sweep-pipeline" error={errors.pipelineId?.message}>
            {solePipeline ? (
              <Input id="sweep-pipeline" value={`${pipelines[0].name} v${pipelines[0].version}`} disabled readOnly />
            ) : (
              <Controller
                name="pipelineId"
                control={control}
                render={({ field }) => (
                  <Select items={items} value={field.value || null} onValueChange={value => field.onChange(value ?? '')}>
                    <SelectTrigger id="sweep-pipeline" aria-invalid={!!errors.pipelineId}><SelectValue placeholder={pipelinesQuery.isPending ? 'Loading pipelines…' : 'Select a pipeline'} /></SelectTrigger>
                    <SelectContent>{pipelines.map(pipeline => <SelectItem key={pipeline.id} value={pipeline.id}>{pipeline.name} v{pipeline.version}</SelectItem>)}</SelectContent>
                  </Select>
                )}
              />
            )}
          </FormField>
          <FormField label="Trial parameters" htmlFor="sweep-trials" error={errors.trials?.message}>
            <div className="space-y-2">
              <Textarea id="sweep-trials" className="min-h-44 font-mono" spellCheck={false} aria-invalid={!!errors.trials} {...register('trials')} />
              <p className="text-xs text-muted-foreground">JSON array; each object becomes one run&apos;s params.</p>
            </div>
          </FormField>
          <FormActions
            status={submitError || (pipelinesQuery.isError ? 'Failed to load pipelines.' : undefined)}
            submitLabel={createSweep.isPending ? 'Creating…' : 'Create Sweep'}
            submitDisabled={createSweep.isPending || pipelinesQuery.isPending || pipelines.length === 0}
            onCancel={() => void navigate({ to: listPath })}
          />
        </form>
      </DataBodyTemplate.Group>
    </DataBodyTemplate>
  )
}
