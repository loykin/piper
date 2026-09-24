import { useNavigate } from '@/lib/router'
import { useMemo, useState } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import { Controller, useForm, useWatch } from 'react-hook-form'
import { z } from 'zod'
import {
  DataBodyTemplate,
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
import { useProjectId } from '@/features/projects/context'
import { useAutoSelectSole } from '@/shared/hooks/useAutoSelectSole'
import { errorMessage } from '@/lib/format'
import { PageCrumbs } from '@/shared/components/PageCrumbs'
import { FormSubmitBar } from '@/shared/components/FormSubmitBar'
import { createCopy } from '@/lib/copy'

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
  const pipelineId = useWatch({ control, name: 'pipelineId' })
  useAutoSelectSole(
    pipelines,
    pipelineId,
    pipeline => pipeline.id,
    v => setValue('pipelineId', v, { shouldValidate: true }),
  )
  const solePipeline = pipelines.length === 1 ? pipelines[0].id : null

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
      void navigate(listPath)
    } catch (cause) {
      setSubmitError(errorMessage(cause))
    }
  }

  return (
    <DataBodyTemplate
      topBar={<PageTopBar left={<PageCrumbs items={['Pipelines', { label: 'Experiments', to: `/projects/${projectId}/experiments` }, createCopy('Sweep').crumb]} />} />}
      title={createCopy('Sweep').title}
      description="Run one saved pipeline with multiple parameter sets under a shared experiment name."
    >
      <DataBodyTemplate.Group title="Sweep">
        <form className="max-w-2xl space-y-6" onSubmit={event => void handleSubmit(submit)(event)}>
          <FormField label="Experiment Name" htmlFor="experiment-name" error={errors.experiment?.message}>
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
          <FormField label="Trial Parameters" htmlFor="sweep-trials" error={errors.trials?.message}>
            <div className="space-y-2">
              <Textarea id="sweep-trials" className="min-h-44 font-mono" spellCheck={false} aria-invalid={!!errors.trials} {...register('trials')} />
              <p className="text-xs text-muted-foreground">JSON array; each object becomes one run&apos;s params.</p>
            </div>
          </FormField>
          <FormSubmitBar
            verb="Create"
            noun="Sweep"
            pending={createSweep.isPending}
            error={submitError || (pipelinesQuery.isError ? 'Failed to load pipelines.' : undefined)}
            disabled={pipelinesQuery.isPending || pipelines.length === 0}
            onCancel={() => void navigate(listPath)}
          />
        </form>
      </DataBodyTemplate.Group>
    </DataBodyTemplate>
  )
}
