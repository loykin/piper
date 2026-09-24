import { useState, type ReactNode } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import { DataBodyTemplate, FormActions, PageTopBar } from '@loykin/designkit'
import { useForm } from 'react-hook-form'
import { z } from 'zod'
import { SecretEntriesField } from '@/features/credentials/components/SecretEntriesField'
import { useCredential, useRotateCredential } from '@/features/credentials/hooks'
import { secretEntriesPayload, secretEntrySchema } from '@/features/credentials/secretEntries'
import type { Credential } from '@/features/credentials/types'
import { useProjectId } from '@/features/projects/context'
import { errorMessage } from '@/lib/format'
import { useNavigate, useParams } from '@/lib/router'
import { PageCrumbs } from '@/shared/components/PageCrumbs'

// A git credential authenticates with a token/password; other kinds only
// need at least one named key (generic credentials may carry empty values).
function rotateSchema(kind: Credential['kind'] | undefined) {
  return z.object({ entries: z.array(secretEntrySchema) }).superRefine(({ entries }, ctx) => {
    const ok = kind === 'git'
      ? entries.some(e => ['token', 'password'].includes(e.key.trim()) && e.value.trim())
      : entries.some(e => e.key.trim())
    if (!ok) {
      ctx.addIssue({
        code: 'custom',
        path: ['entries'],
        message: kind === 'git' ? 'Enter a new token or password.' : 'Enter at least one key.',
      })
    }
  })
}

type RotateValues = z.infer<ReturnType<typeof rotateSchema>>

function initialEntries(kind: Credential['kind'] | undefined): RotateValues['entries'] {
  return kind === 'git'
    ? [{ key: 'username', value: '' }, { key: 'token', value: '' }]
    : [{ key: '', value: '' }]
}

/** Replaces a project credential's secret values. */
export default function CredentialRotatePage() {
  const { name = '' } = useParams<{ name: string }>()
  const credential = useCredential(name)
  // Mount the form only once the kind is known, so its defaults and
  // validation rule match the credential being rotated.
  return credential.data
    ? <RotateForm credential={credential.data} />
    : <RotateShell name={name} error={credential.isError ? errorMessage(credential.error) : ''} />
}

function RotateShell({ name, error, children }: { name: string; error?: string; children?: ReactNode }) {
  const projectId = useProjectId()
  return (
    <DataBodyTemplate
      topBar={<PageTopBar left={<PageCrumbs items={['Infrastructure', { label: 'Credentials', to: `/projects/${projectId}/credentials` }, name, 'Rotate']} />} />}
      title={`Rotate ${name}`}
      description="Replace the stored secret values. The previous values are overwritten and cannot be recovered."
    >
      <DataBodyTemplate.Group layout="stacked" title="Secret values" description="Values are write-only; the server never returns them.">
        {children ?? (error
          ? <p className="text-sm text-destructive">{error}</p>
          : <p className="text-sm text-muted-foreground">Loading…</p>)}
      </DataBodyTemplate.Group>
    </DataBodyTemplate>
  )
}

function RotateForm({ credential }: { credential: Credential }) {
  const navigate = useNavigate()
  const projectId = useProjectId()
  const rotate = useRotateCredential()
  const [submitError, setSubmitError] = useState('')
  const { control, register, handleSubmit, formState: { errors } } = useForm<RotateValues>({
    resolver: zodResolver(rotateSchema(credential.kind)),
    defaultValues: { entries: initialEntries(credential.kind) },
  })
  const listPath = `/projects/${projectId}/credentials`

  async function submit(values: RotateValues) {
    setSubmitError('')
    const data = secretEntriesPayload(credential.kind, values.entries)
    try {
      await rotate.mutateAsync({ name: credential.name, data })
      void navigate(listPath)
    } catch (cause) {
      setSubmitError(errorMessage(cause))
    }
  }

  return (
    <RotateShell name={credential.name}>
      <form id="rotate-credential-form" className="space-y-3" noValidate onSubmit={handleSubmit(submit)}>
        <SecretEntriesField
          control={control}
          register={register}
          keyPlaceholder="token"
          error={errors.entries?.root?.message ?? errors.entries?.message}
        />
        <FormActions
          status={submitError || undefined}
          submitLabel={rotate.isPending ? 'Rotating…' : 'Rotate'}
          submitDisabled={rotate.isPending}
          onCancel={() => void navigate(listPath)}
        />
      </form>
    </RotateShell>
  )
}
