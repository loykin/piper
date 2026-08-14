import { useEffect, useMemo, useState } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import {
  Button,
  DataBodyTemplate,
  Input,
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
import { useFederationMembers } from '@/features/federation/hooks'
import { useCreateProject } from '@/features/projects/hooks'
import { useNavigate } from '@/lib/router'

const projectSchema = z.object({
  id: z.string().trim().min(1, 'Project ID is required.').regex(/^[a-z0-9][a-z0-9-]{0,62}$/, 'Use lowercase letters, numbers, and hyphens only.'),
  name: z.string().trim().min(1, 'Project name is required.'),
  description: z.string().trim(),
  ownerMemberID: z.string().trim().min(1, 'Owner Member is required.'),
})

type ProjectValues = z.infer<typeof projectSchema>

export default function ProjectCreatePage() {
  const navigate = useNavigate()
  const createProject = useCreateProject()
  const membersQuery = useFederationMembers()
  const members = useMemo(
    () => (membersQuery.data ?? []).filter(member => member.enabled),
    [membersQuery.data],
  )
  const [submitError, setSubmitError] = useState('')
  const {
    control,
    register,
    handleSubmit,
    setValue,
    formState: { errors },
  } = useForm<ProjectValues>({
    resolver: zodResolver(projectSchema),
    defaultValues: { id: '', name: '', description: '', ownerMemberID: 'member-local' },
  })

  useEffect(() => {
    if (members.length > 0 && !members.some(member => member.id === 'member-local')) {
      setValue('ownerMemberID', members[0].id)
    }
  }, [members, setValue])

  async function submit(values: ProjectValues) {
    setSubmitError('')
    try {
      const project = await createProject.mutateAsync({
        id: values.id,
        name: values.name,
        description: values.description,
        owner_member_id: values.ownerMemberID,
      })
      void navigate(`/projects/${project.id}/schedules`, { replace: true })
    } catch (cause) {
      setSubmitError(cause instanceof Error ? cause.message : String(cause))
    }
  }

  return (
    <DataBodyTemplate
      topBar={<PageTopBar left="Projects / New Project" />}
      title="New Project"
      description="Create the Home directory entry and choose the Member that owns its execution state."
    >
      <DataBodyTemplate.Group
        layout="stacked"
        title="Project directory"
        description="The Owner Member stores and executes this project's pipelines, runs, schedules, notebooks, and services."
      >
        <form className="space-y-3" noValidate onSubmit={handleSubmit(submit)}>
          <div className="space-y-1.5">
            <Label htmlFor="project-id" className="text-xs">Project ID</Label>
            <Input id="project-id" placeholder="my-project" className="h-8 text-sm" aria-invalid={!!errors.id} {...register('id')} />
            {errors.id && <p className="text-xs text-destructive">{errors.id.message}</p>}
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="project-name" className="text-xs">Name</Label>
            <Input id="project-name" placeholder="My Project" className="h-8 text-sm" aria-invalid={!!errors.name} {...register('name')} />
            {errors.name && <p className="text-xs text-destructive">{errors.name.message}</p>}
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="project-description" className="text-xs">Description</Label>
            <Input id="project-description" className="h-8 text-sm" {...register('description')} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="project-owner-member" className="text-xs">Owner Member</Label>
            <Controller
              name="ownerMemberID"
              control={control}
              render={({ field }) => (
                <Select value={field.value} onValueChange={field.onChange} disabled={membersQuery.isLoading}>
                  <SelectTrigger id="project-owner-member" size="sm" aria-invalid={!!errors.ownerMemberID}>
                    <SelectValue placeholder={membersQuery.isLoading ? 'Loading Members…' : 'Select a Member'} />
                  </SelectTrigger>
                  <SelectContent>
                    {(members.length > 0 ? members : [{ id: 'member-local', status: 'offline' as const }]).map(member => (
                      <SelectItem key={member.id} value={member.id}>
                        {member.id} ({member.status})
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            />
            {errors.ownerMemberID && <p className="text-xs text-destructive">{errors.ownerMemberID.message}</p>}
            {membersQuery.isError && (
              <p className="text-xs text-muted-foreground">Federation directory is unavailable; Local Member will be used.</p>
            )}
          </div>
          {submitError && <p className="text-sm text-destructive" role="alert">{submitError}</p>}
          <div className="flex justify-end gap-2 border-t border-border pt-(--designkit-panel-gap)">
            <Button type="button" variant="outline" size="sm" className="h-8 text-xs" onClick={() => void navigate(-1)}>Cancel</Button>
            <Button type="submit" size="sm" className="h-8 text-xs" disabled={createProject.isPending}>
              {createProject.isPending ? 'Creating…' : 'Create Project'}
            </Button>
          </div>
        </form>
      </DataBodyTemplate.Group>
    </DataBodyTemplate>
  )
}
