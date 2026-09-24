import { useState } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import {
  DataBodyTemplate,
  FormField,
  Input,
  Label,
  PageTopBar,
  Switch,
} from '@loykin/designkit'
import { Controller, useForm } from 'react-hook-form'
import { z } from 'zod'
import { usePipeline } from '@/features/pipelines/hooks'
import { useProjectId } from '@/features/projects/context'
import { useDeployTemplate } from '@/features/schedules/hooks'
import { errorMessage } from '@/lib/format'
import { maxRunsField, toMaxRuns } from '@/features/schedules/maxRuns'
import { PageCrumbs } from '@/shared/components/PageCrumbs'
import { useNavigate, useParams } from '@/lib/router'
import { FormSubmitBar } from '@/shared/components/FormSubmitBar'
import { PageState } from '@/shared/components/PageState'

const deploySchema = z.object({
  cron: z.string().trim().min(1, 'Cron expression is required.'),
  enabled: z.boolean(),
  maxRuns: maxRunsField,
})

type DeployValues = z.infer<typeof deploySchema>

/** Deploys one pipeline template version as a new cron schedule. */
export default function ScheduleDeployPage() {
  const navigate = useNavigate()
  const projectId = useProjectId()
  const { id = '' } = useParams<{ id: string }>()
  const template = usePipeline(id)
  const deploy = useDeployTemplate()
  const [submitError, setSubmitError] = useState('')
  const { control, register, handleSubmit, formState: { errors } } = useForm<DeployValues>({
    resolver: zodResolver(deploySchema),
    defaultValues: { cron: '0 2 * * *', enabled: true, maxRuns: '' },
  })

  const templatesPath = `/projects/${projectId}/pipelines`
  const templateLabel = template.data ? `${template.data.name} v${template.data.version}` : id

  async function submit(values: DeployValues) {
    setSubmitError('')
    try {
      const schedule = await deploy.mutateAsync({
        templateId: id,
        req: { cron: values.cron, enabled: values.enabled, max_runs: toMaxRuns(values.maxRuns) },
      })
      void navigate(`/projects/${projectId}/schedules/${schedule.id}`)
    } catch (cause) {
      setSubmitError(errorMessage(cause))
    }
  }

  const topBar = <PageTopBar left={<PageCrumbs items={['Pipelines', { label: 'Templates', to: templatesPath }, templateLabel, 'Deploy']} />} />
  if (!template.data) {
    return <PageState query={template} noun="pipeline template" template="data" topBar={topBar} />
  }

  return (
    <DataBodyTemplate
      topBar={topBar}
      title="Deploy to Schedule"
      description={`Creates a new cron schedule from ${templateLabel}. The schedule is independent — updating the template later does not affect it.`}
    >
      <DataBodyTemplate.Group layout="stacked" title="Schedule" description="When the schedule fires and how many completed runs it keeps.">
        <form id="deploy-schedule-form" className="space-y-3" noValidate onSubmit={handleSubmit(submit)}>
          <FormField label="Cron Expression" htmlFor="deploy-cron" error={errors.cron?.message}>
            <Input
              id="deploy-cron"
              className="h-8 font-mono text-sm"
              aria-invalid={!!errors.cron}
              {...register('cron')}
            />
          </FormField>

          <FormField
            label="Retention"
            htmlFor="deploy-max-runs"
            error={errors.maxRuns?.message}
            helperText="Completed run records to keep, not a limit on how many times this schedule fires. Leave blank or 0 to keep all of them."
          >
            <Input
              id="deploy-max-runs"
              type="number"
              min={0}
              step={1}
              className="h-8 text-sm"
              aria-invalid={!!errors.maxRuns}
              {...register('maxRuns')}
            />
          </FormField>

          <Controller
            name="enabled"
            control={control}
            render={({ field }) => (
              <div className="flex items-center justify-between">
                <div>
                  <Label htmlFor="deploy-enabled" className="text-sm">Enable Immediately</Label>
                  <p className="text-xs text-muted-foreground">A disabled schedule is saved but never fires until enabled.</p>
                </div>
                <Switch id="deploy-enabled" checked={field.value} onCheckedChange={field.onChange} />
              </div>
            )}
          />

          <FormSubmitBar
            verb="Create"
            noun="Schedule"
            pending={deploy.isPending}
            error={submitError}
            onCancel={() => void navigate(templatesPath)}
          />
        </form>
      </DataBodyTemplate.Group>
    </DataBodyTemplate>
  )
}
