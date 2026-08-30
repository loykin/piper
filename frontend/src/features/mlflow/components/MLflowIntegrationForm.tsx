import { zodResolver } from '@hookform/resolvers/zod'
import { Controller, useForm } from 'react-hook-form'
import { z } from 'zod'
import { DataBodyTemplate, FormActions, FormField, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@loykin/designkit'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { useCredentials } from '@/features/credentials/hooks'
import type { MLflowIntegration, MLflowIntegrationRequest } from '../types'

const schema = z.object({
  name: z.string().trim().min(1, 'Name is required.'),
  tracking_uri: z.string().trim().url('Enter a valid Tracking Server URL.'),
  credential_ref: z.string().trim().min(1, 'Credential is required.'),
  experiment_template: z.string().trim().min(1, 'Experiment template is required.'),
  enabled: z.boolean(),
  default: z.boolean(),
  export_pipelines: z.boolean(),
  export_notebook_executions: z.boolean(),
})
type Values = z.infer<typeof schema>

export function MLflowIntegrationForm({ initial, busy, error, onSubmit, onCancel }: { initial?: MLflowIntegration; busy: boolean; error?: string; onSubmit: (value: MLflowIntegrationRequest) => Promise<void>; onCancel: () => void }) {
  const credentials = useCredentials()
  // Keep the integration's current credential in the list even if it has
  // since been disabled, so editing an integration whose credential was
  // disabled elsewhere doesn't render the field as if the reference were
  // lost — the disabled suffix still communicates the real state.
  const mlflowCredentials = (credentials.data ?? []).filter(item => item.kind === 'mlflow' && (!item.disabled || item.name === initial?.credential_ref))
  const { control, register, handleSubmit, formState: { errors } } = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: {
      name: initial?.name ?? '', tracking_uri: initial?.tracking_uri ?? '', credential_ref: initial?.credential_ref ?? '',
      experiment_template: initial?.experiment_template || 'piper/{project_id}/{experiment_or_pipeline}',
      enabled: initial?.enabled ?? true, default: initial?.default ?? true,
      export_pipelines: initial?.export_pipelines ?? true,
      export_notebook_executions: initial?.export_notebook_executions ?? false,
    },
  })
  return <form className="space-y-4" noValidate onSubmit={handleSubmit(value => onSubmit({ ...value, artifact_mode: 'reference' }))}>
    <FormField label="Name" htmlFor="mlflow-name" error={errors.name?.message}><Input id="mlflow-name" {...register('name')} placeholder="production-mlflow" /></FormField>
    <FormField label="Tracking URI" htmlFor="mlflow-uri" error={errors.tracking_uri?.message} helperText="HTTPS is required unless the server explicitly allows insecure local HTTP."><Input id="mlflow-uri" className="font-mono" {...register('tracking_uri')} placeholder="https://mlflow.example.com" /></FormField>
    <FormField label="Credential" htmlFor="mlflow-credential" error={errors.credential_ref?.message} helperText={mlflowCredentials.length === 0 ? 'Create an MLflow credential before configuring the integration.' : 'Credential values stay write-only; only this reference is stored here.'}>
      <Controller name="credential_ref" control={control} render={({ field }) => <Select value={field.value || undefined} onValueChange={field.onChange} disabled={mlflowCredentials.length === 0}><SelectTrigger id="mlflow-credential" className="w-72"><SelectValue placeholder="Select a credential" /></SelectTrigger><SelectContent>{mlflowCredentials.map(item => <SelectItem key={item.name} value={item.name}>{item.name}{item.disabled ? ' (disabled)' : ''}</SelectItem>)}</SelectContent></Select>} />
    </FormField>
    <FormField label="Experiment template" htmlFor="mlflow-template" error={errors.experiment_template?.message}><Input id="mlflow-template" className="font-mono" {...register('experiment_template')} /></FormField>
    <DataBodyTemplate.Group layout="stacked" title="Export scope" description="Artifacts remain authoritative in Piper; MLflow receives references.">
      {([['enabled', 'Enable integration'], ['default', 'Use as project default'], ['export_pipelines', 'Export pipeline runs'], ['export_notebook_executions', 'Export notebook executions']] as const).map(([name, label]) => <Controller key={name} name={name} control={control} render={({ field }) => <div className="flex items-center justify-between rounded-md border border-border p-3"><span className="text-sm">{label}</span><Switch checked={field.value} onCheckedChange={field.onChange} aria-label={label} /></div>} />)}
    </DataBodyTemplate.Group>
    <FormActions status={error} submitLabel={busy ? 'Saving…' : 'Save Integration'} submitDisabled={busy} onCancel={onCancel} />
  </form>
}
