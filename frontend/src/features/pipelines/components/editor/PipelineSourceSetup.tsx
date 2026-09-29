import { useMemo } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import {
  FormField, Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from '@loykin/designkit'
import { Controller, useForm, useWatch } from 'react-hook-form'
import { z } from 'zod'
import { Input } from '@/components/ui/input'
import { useCredentials } from '@/features/credentials/hooks'
import type { NotebookVolume } from '@/features/notebooks/types'
import { useAutoSelectSole } from '@/shared/hooks/useAutoSelectSole'
import { autoMatchGitCredential, type SourceKind } from '../../editorModel'
import { FormSubmitBar } from '@/shared/components/FormSubmitBar'

const setupSchema = z.object({
  name: z.string(),
  sourceKind: z.enum(['notebook-volume', 'git', 'none']),
  volumeId: z.string(),
  credential: z.string(),
  repo: z.string(),
  branch: z.string(),
}).superRefine((v, ctx) => {
  if (v.sourceKind === 'notebook-volume' && !v.volumeId) {
    ctx.addIssue({ code: 'custom', path: ['volumeId'], message: 'Select a notebook volume.' })
  } else if (v.sourceKind === 'git' && !v.repo.trim()) {
    ctx.addIssue({ code: 'custom', path: ['repo'], message: 'Repository URL is required.' })
  }
})

export type PipelineSourceSetupValues = z.infer<typeof setupSchema>

/**
 * Locks in the pipeline's single source workspace before the editor opens.
 * `onStart` receives the URL search params the editor reads its source from.
 */
export function PipelineSourceSetup({
  initial, volumes, onStart, onCancel,
}: {
  initial: PipelineSourceSetupValues
  volumes: NotebookVolume[]
  onStart: (params: Record<string, string>, name: string) => void
  onCancel: () => void
}) {
  const { control, register, handleSubmit, setValue, getValues, formState: { errors } } = useForm<PipelineSourceSetupValues>({
    resolver: zodResolver(setupSchema),
    defaultValues: initial,
  })
  const [sourceKind, volumeId, credential, repo] = useWatch({ control, name: ['sourceKind', 'volumeId', 'credential', 'repo'] })

  // Auto-apply the sole volume instead of relying on an interaction
  // @base-ui/react's Select won't commit when it has exactly one item.
  useAutoSelectSole(volumes, volumeId, v => v.id, id => setValue('volumeId', id), { skip: sourceKind !== 'notebook-volume' })

  const { data: credentials = [] } = useCredentials()
  // Keep the chosen credential listed even if it has since been disabled, so
  // it doesn't render as if the reference were lost.
  const gitCredentials = useMemo(
    () => credentials.filter(c => c.kind === 'git' && (!c.disabled || c.name === credential)),
    [credentials, credential],
  )
  // With no explicit credential, preview which one the backend will
  // auto-match by endpoint scope so the user knows the source is authenticated.
  const autoMatched = credential ? undefined : autoMatchGitCredential(gitCredentials, repo)

  function submit(v: PipelineSourceSetupValues) {
    const name = v.name.trim() || 'my-pipeline'
    const params: Record<string, string> = { source: v.sourceKind, name }
    if (v.sourceKind === 'notebook-volume') params.volume = v.volumeId
    else if (v.sourceKind === 'git') {
      if (v.credential.trim()) params.credential = v.credential.trim()
      params.repo = v.repo.trim()
      if (v.branch.trim()) params.branch = v.branch.trim()
    }
    onStart(params, name)
  }

  return (
    <form className="space-y-3" noValidate onSubmit={handleSubmit(submit)}>
      <FormField label="Pipeline Name" htmlFor="pipeline-name">
        <Input id="pipeline-name" {...register('name')} />
      </FormField>
      <FormField
        label="Source Type"
        htmlFor="pipeline-source-type"
        helperText={sourceKind === 'none' ? 'Command tasks only — Python and notebook tasks need a volume or repository to run from.' : undefined}
      >
        <Controller
          name="sourceKind"
          control={control}
          render={({ field }) => (
            <Select
              items={[
                { value: 'notebook-volume', label: 'Notebook Volume' },
                { value: 'git', label: 'Git Repository' },
                { value: 'none', label: 'None (commands only)' },
              ]}
              value={field.value}
              onValueChange={v => field.onChange(v as SourceKind)}
            >
              <SelectTrigger id="pipeline-source-type"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="notebook-volume">Notebook Volume</SelectItem>
                <SelectItem value="git">Git Repository</SelectItem>
                <SelectItem value="none">None (commands only)</SelectItem>
              </SelectContent>
            </Select>
          )}
        />
      </FormField>
      {sourceKind === 'notebook-volume' ? (
        <FormField label="Notebook Volume" htmlFor="pipeline-volume" error={errors.volumeId?.message}>
          <Controller
            name="volumeId"
            control={control}
            render={({ field }) => (
              <Select
                items={volumes.length === 0
                  ? [{ value: '__none__', label: 'No released volumes' }]
                  : volumes.map(v => ({ value: v.id, label: `${v.label} · ${v.work_dir}` }))}
                value={field.value}
                onValueChange={v => field.onChange(v ?? '')}
              >
                <SelectTrigger id="pipeline-volume" className="w-full" aria-invalid={!!errors.volumeId}><SelectValue placeholder="— select a volume —" /></SelectTrigger>
                <SelectContent>
                  {volumes.length === 0 ? (
                    <SelectItem value="__none__" disabled>No released volumes</SelectItem>
                  ) : volumes.map(v => (
                    <SelectItem key={v.id} value={v.id}>{v.label} · {v.work_dir}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          />
        </FormField>
      ) : sourceKind === 'git' ? (
        <div className="space-y-3">
          <FormField
            label={<>Git Credential <span className="font-normal text-muted-foreground/70">(optional)</span></>}
            htmlFor="pipeline-git-credential"
            helperText={
              credential
                ? 'Using the selected credential.'
                : autoMatched
                  ? `Auto-matched: ${autoMatched.name} · ${autoMatched.endpoint}`
                  : 'Leave empty to auto-match a registered credential, or clone unauthenticated.'
            }
          >
            {/* value must never be `undefined` here — see MLflowIntegrationForm.tsx's
                Credential Select: an undefined→defined value is an illegal
                uncontrolled-to-controlled transition for @base-ui/react's Select,
                which then desyncs its displayed value. */}
            <Controller
              name="credential"
              control={control}
              render={({ field }) => (
                <Select
                  items={gitCredentials.length === 0
                    ? [{ value: '__none__', label: 'No active git credentials' }]
                    : gitCredentials.map(c => ({
                      value: c.name,
                      label: `${c.name}${c.disabled ? ' (disabled)' : ''} · ${c.endpoint || 'any repo'}`,
                    }))}
                  value={field.value || null}
                  onValueChange={v => {
                    const name = v ?? ''
                    field.onChange(name)
                    const picked = gitCredentials.find(c => c.name === name)
                    if (picked?.endpoint && !getValues('repo').trim()) setValue('repo', picked.endpoint)
                  }}
                >
                  <SelectTrigger id="pipeline-git-credential" className="w-full"><SelectValue placeholder="Auto-match by repository URL" /></SelectTrigger>
                  <SelectContent>
                    {gitCredentials.length === 0 ? (
                      <SelectItem value="__none__" disabled>No active git credentials</SelectItem>
                    ) : gitCredentials.map(c => (
                      <SelectItem key={c.name} value={c.name}>{c.name}{c.disabled ? ' (disabled)' : ''} · {c.endpoint || 'any repo'}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            />
          </FormField>
          <FormField label="Repository URL" htmlFor="pipeline-git-repo" error={errors.repo?.message}>
            <Input id="pipeline-git-repo" placeholder="https://github.com/org/repo.git" aria-invalid={!!errors.repo} {...register('repo')} />
          </FormField>
          <FormField label="Branch" htmlFor="pipeline-git-branch">
            <Input id="pipeline-git-branch" placeholder="main" {...register('branch')} />
          </FormField>
        </div>
      ) : null}
      <FormSubmitBar verb="Start" noun="Editing" pending={false} onCancel={onCancel} />
    </form>
  )
}
