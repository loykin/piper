import { useState } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
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
import { Controller, useForm } from 'react-hook-form'
import { z } from 'zod'
import { useAddMember, useMemberCandidates } from '@/features/access/hooks'
import { useProjectId } from '@/lib/projectContext'
import { useNavigate } from '@/lib/router'

const memberSchema = z.object({
  username: z.string().trim().min(1, 'Username is required.'),
  role: z.enum(['viewer', 'member', 'admin']),
})

type MemberValues = z.infer<typeof memberSchema>

export default function MemberCreatePage() {
  const projectId = useProjectId()
  const navigate = useNavigate()
  const addMember = useAddMember()
  const { data: candidates = [], isLoading: candidatesLoading } = useMemberCandidates()
  const [submitError, setSubmitError] = useState('')
  const {
    control,
    handleSubmit,
    formState: { errors },
  } = useForm<MemberValues>({
    resolver: zodResolver(memberSchema),
    defaultValues: { username: '', role: 'member' },
  })

  const listPath = `/projects/${projectId}/members`

  async function submit(values: MemberValues) {
    setSubmitError('')
    try {
      await addMember.mutateAsync(values)
      void navigate(listPath)
    } catch (cause) {
      setSubmitError(cause instanceof Error ? cause.message : String(cause))
    }
  }

  return (
    <DataBodyTemplate
      topBar={<PageTopBar left="Project Members / New Member" />}
      title="New Member"
      description="Grant an existing Piper account access to this project."
    >
      <DataBodyTemplate.Group
        layout="stacked"
        title="Project access"
        description="Select an existing account and choose its project-specific role."
      >
        <form className="space-y-3" noValidate onSubmit={handleSubmit(submit)}>
          <FormField
            label="Username"
            htmlFor="member-username"
            error={errors.username?.message}
            helperText={!candidatesLoading && candidates.length === 0 ? 'No accounts are available to add.' : undefined}
          >
            <Controller
              name="username"
              control={control}
              render={({ field }) => (
                <Select value={field.value} onValueChange={field.onChange} disabled={candidatesLoading}>
                  <SelectTrigger id="member-username" size="sm" aria-invalid={!!errors.username}>
                    <SelectValue placeholder={candidatesLoading ? 'Loading users…' : 'Select a user'} />
                  </SelectTrigger>
                  <SelectContent>
                    {candidates.map(candidate => (
                      <SelectItem key={candidate.username} value={candidate.username}>
                        {candidate.username}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            />
          </FormField>
          <FormField
            label="Project role"
            htmlFor="member-role"
            helperText="Viewer can inspect resources, Member can operate workloads, and Admin can manage project access."
          >
            <Controller
              name="role"
              control={control}
              render={({ field }) => (
                <Select value={field.value} onValueChange={field.onChange}>
                  <SelectTrigger id="member-role" className="h-8 w-44 text-sm">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="viewer">Viewer</SelectItem>
                    <SelectItem value="member">Member</SelectItem>
                    <SelectItem value="admin">Admin</SelectItem>
                  </SelectContent>
                </Select>
              )}
            />
          </FormField>
          <FormActions
            status={submitError || undefined}
            submitLabel={addMember.isPending ? 'Adding…' : 'Add Member'}
            submitDisabled={addMember.isPending || candidates.length === 0}
            onCancel={() => void navigate(listPath)}
          />
        </form>
      </DataBodyTemplate.Group>
    </DataBodyTemplate>
  )
}
