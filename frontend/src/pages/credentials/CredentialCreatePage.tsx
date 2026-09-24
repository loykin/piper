import { useState } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import { DataBodyTemplate, FormField, PageTopBar, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@loykin/designkit'
import { Controller, useForm, useWatch } from 'react-hook-form'
import { z } from 'zod'
import { Input } from '@/components/ui/input'
import { useSearchParams, useNavigate } from '@/lib/router'
import { useProjectId } from '@/features/projects/context'
import { SecretEntriesField } from '@/features/credentials/components/SecretEntriesField'
import { useCreateCredential } from '@/features/credentials/hooks'
import { secretEntriesPayload, secretEntrySchema, type SecretEntry } from '@/features/credentials/secretEntries'
import type { CredentialKind } from '@/features/credentials/types'
import { errorMessage } from '@/lib/format'
import { PageCrumbs } from '@/shared/components/PageCrumbs'
import { FormSubmitBar } from '@/shared/components/FormSubmitBar'
import { createCopy } from '@/lib/copy'

const CREDENTIAL_KINDS: CredentialKind[] = ['generic', 'git', 's3', 'gcs', 'azure', 'slack', 'webhook', 'mlflow']

function initialKindFromSearch(searchParams: URLSearchParams): CredentialKind {
  const requested = searchParams.get('kind')
  return (CREDENTIAL_KINDS as string[]).includes(requested ?? '') ? (requested as CredentialKind) : 'generic'
}

type DataEntry = SecretEntry
const emptyEntry = (): DataEntry => ({ key: '', value: '' })

const GENERIC_FIELDS: DataEntry[] = [emptyEntry()]
const GIT_FIELDS: DataEntry[] = [
  { key: 'username', value: '' },
  { key: 'token', value: '' },
]
const SLACK_FIELDS: DataEntry[] = [{ key: 'webhook_url', value: '' }]
const WEBHOOK_FIELDS: DataEntry[] = [
  { key: 'url', value: '' },
  { key: 'header_Authorization', value: '' },
]
const MLFLOW_FIELDS: DataEntry[] = [{ key: 'token', value: '' }]
// s3 credentials are system-scoped (managed on the Storage page). This
// project-scoped form creates source, notification, and MLflow credentials.
function fieldsForKind(kind: CredentialKind): DataEntry[] {
  if (kind === 'git') return GIT_FIELDS.map(e => ({ ...e }))
  if (kind === 'slack') return SLACK_FIELDS.map(e => ({ ...e }))
  if (kind === 'webhook') return WEBHOOK_FIELDS.map(e => ({ ...e }))
  if (kind === 'mlflow') return MLFLOW_FIELDS.map(e => ({ ...e }))
  return GENERIC_FIELDS.map(e => ({ ...e }))
}

const createCredentialSchema = z.object({
  name: z.string().trim().min(1, 'Name is required.'),
  kind: z.enum(['generic', 'git', 's3', 'gcs', 'azure', 'slack', 'webhook', 'mlflow']),
  endpoint: z.string(),
  entries: z.array(secretEntrySchema),
}).superRefine((values, ctx) => {
  if (Object.keys(secretEntriesPayload(values.kind, values.entries)).length === 0) {
    ctx.addIssue({
      code: 'custom',
      path: ['entries'],
      message: values.kind === 'generic' ? 'Enter at least one key.' : 'Enter at least one key with a value.',
    })
  }
})

type CreateCredentialValues = z.infer<typeof createCredentialSchema>

export default function CredentialCreatePage() {
  const projectId = useProjectId()
  const navigate = useNavigate()
  const createCredential = useCreateCredential()
  const [searchParams] = useSearchParams()
  const [submitError, setSubmitError] = useState('')
  const listPath = `/projects/${projectId}/credentials`

  const initialKind = initialKindFromSearch(searchParams)
  const { control, register, handleSubmit, reset, getValues, formState: { errors } } = useForm<CreateCredentialValues>({
    resolver: zodResolver(createCredentialSchema),
    defaultValues: { name: '', kind: initialKind, endpoint: '', entries: fieldsForKind(initialKind) },
  })
  const kind = useWatch({ control, name: 'kind' })

  function handleKindChange(next: CredentialKind) {
    // Each kind has its own expected keys; keep only the name typed so far.
    reset({ name: getValues('name'), kind: next, endpoint: '', entries: fieldsForKind(next) })
  }

  async function submit(values: CreateCredentialValues) {
    setSubmitError('')
    try {
      await createCredential.mutateAsync({
        name: values.name.trim(),
        kind: values.kind,
        endpoint: values.kind === 'git' ? values.endpoint.trim() : undefined,
        data: secretEntriesPayload(values.kind, values.entries),
      })
      void navigate(listPath)
    } catch (cause) {
      setSubmitError(errorMessage(cause))
    }
  }

  const entriesError = errors.entries?.root?.message ?? errors.entries?.message

  return (
    <DataBodyTemplate
      topBar={<PageTopBar left={<PageCrumbs items={['Infrastructure', { label: 'Credentials', to: listPath }, createCopy('Credential').crumb]} />} />}
      title={createCopy('Credential').title}
      description="Create a write-only credential. Stored values are never returned by the API."
    >
      <DataBodyTemplate.Group
        layout="stacked"
        title="Credential"
        description="Stored values are write-only — the API never returns them again."
      >
        <form className="space-y-3" noValidate onSubmit={handleSubmit(submit)}>
          <FormField label="Name" htmlFor="credential-name" error={errors.name?.message}>
            <Input
              id="credential-name"
              placeholder={kind === 'git' ? 'github-acme' : 'wandb'}
              className="h-8 font-mono text-sm"
              aria-invalid={!!errors.name}
              {...register('name')}
            />
          </FormField>

          <FormField label="Kind" htmlFor="credential-kind">
            <Controller
              name="kind"
              control={control}
              render={({ field }) => (
                <Select
                  items={[
                    { value: 'generic', label: 'Generic' },
                    { value: 'git', label: 'Git' },
                    { value: 'slack', label: 'Slack' },
                    { value: 'webhook', label: 'Webhook' },
                    { value: 'mlflow', label: 'MLflow' },
                  ]}
                  value={field.value}
                  onValueChange={value => handleKindChange((value ?? 'generic') as CredentialKind)}
                >
                  <SelectTrigger id="credential-kind" className="h-8 w-44 text-sm">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="generic">Generic</SelectItem>
                    <SelectItem value="git">Git</SelectItem>
                    <SelectItem value="slack">Slack</SelectItem>
                    <SelectItem value="webhook">Webhook</SelectItem>
                    <SelectItem value="mlflow">MLflow</SelectItem>
                  </SelectContent>
                </Select>
              )}
            />
          </FormField>

          {kind === 'git' && (
            <FormField
              label={<>Endpoint URL prefix <span className="ml-1 text-xs text-muted-foreground">(optional)</span></>}
              htmlFor="credential-endpoint"
            >
              <Input
                id="credential-endpoint"
                placeholder="https://github.com/myorg/"
                className="h-8 font-mono text-sm"
                {...register('endpoint')}
              />
            </FormField>
          )}

          <SecretEntriesField
            control={control}
            register={register}
            label={kind === 'generic' ? 'Data' : kind === 'git' || kind === 'mlflow' ? 'Credentials' : 'Notification endpoint'}
            keyPlaceholder={kind === 'git' || kind === 'mlflow' ? 'token' : kind === 'slack' ? 'webhook_url' : kind === 'webhook' ? 'url' : 'api_key'}
            error={entriesError}
          />

          <FormSubmitBar
            verb="Create"
            noun="Credential"
            pending={createCredential.isPending}
            error={submitError}
            onCancel={() => void navigate(listPath)}
          />
        </form>
      </DataBodyTemplate.Group>
    </DataBodyTemplate>
  )
}
