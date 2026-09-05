import { useEffect, useMemo, useState } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import {
  DataBodyTemplate,
  FormActions,
  FormField,
  Input,
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
          <FormField label="Project ID" htmlFor="project-id" error={errors.id?.message}>
            <Input id="project-id" placeholder="my-project" className="h-8 text-sm" aria-invalid={!!errors.id} {...register('id')} />
          </FormField>
          <FormField label="Name" htmlFor="project-name" error={errors.name?.message}>
            <Input id="project-name" placeholder="My Project" className="h-8 text-sm" aria-invalid={!!errors.name} {...register('name')} />
          </FormField>
          <FormField label="Description" htmlFor="project-description">
            <Input id="project-description" className="h-8 text-sm" {...register('description')} />
          </FormField>
          <FormField
            label="Owner Member"
            htmlFor="project-owner-member"
            error={errors.ownerMemberID?.message}
            helperText={membersQuery.isError ? 'Federation directory is unavailable; Local Member will be used.' : undefined}
          >
            <Controller
              name="ownerMemberID"
              control={control}
              render={({ field }) => (
                <Select
                  items={(members.length > 0 ? members : [{ id: 'member-local', status: 'offline' as const }]).map(member => ({
                    value: member.id,
                    label: `${member.id} (${member.status})`,
                  }))}
                  value={field.value}
                  onValueChange={field.onChange}
                  disabled={membersQuery.isLoading}
                >
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
          </FormField>
          <FormActions
            status={submitError || undefined}
            submitLabel={createProject.isPending ? 'Creating…' : 'Create Project'}
            submitDisabled={createProject.isPending}
            onCancel={() => void navigate(-1)}
          />
        </form>
      </DataBodyTemplate.Group>
    </DataBodyTemplate>
  )
}
