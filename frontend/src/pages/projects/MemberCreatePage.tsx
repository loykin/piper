import { useState } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import {
  Button,
  DataBodyTemplate,
  Label,
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
          <div className="space-y-1.5">
            <Label htmlFor="member-username" className="text-xs">Username</Label>
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
            {errors.username && <p className="text-xs text-destructive">{errors.username.message}</p>}
            {!candidatesLoading && candidates.length === 0 && (
              <p className="text-xs text-muted-foreground">No accounts are available to add.</p>
            )}
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="member-role" className="text-xs">Project role</Label>
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
            <p className="text-xs text-muted-foreground">
              Viewer can inspect resources, Member can operate workloads, and Admin can manage project access.
            </p>
          </div>
          {submitError && <p className="text-sm text-destructive" role="alert">{submitError}</p>}
          <div className="flex justify-end gap-2 border-t border-border pt-(--designkit-panel-gap)">
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-8 text-xs"
              onClick={() => void navigate(listPath)}
            >
              Cancel
            </Button>
            <Button
              type="submit"
              size="sm"
              className="h-8 text-xs"
              disabled={addMember.isPending || candidates.length === 0}
            >
              {addMember.isPending ? 'Adding…' : 'Add Member'}
            </Button>
          </div>
        </form>
      </DataBodyTemplate.Group>
    </DataBodyTemplate>
  )
}
