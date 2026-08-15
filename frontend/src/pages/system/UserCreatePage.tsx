import { useState } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import {
  DataBodyTemplate,
  FormActions,
  FormField,
  Input,
  Label,
  PageTopBar,
  Switch,
} from '@loykin/designkit'
import { Controller, useForm } from 'react-hook-form'
import { z } from 'zod'
import { useCreateUser } from '@/features/access/hooks'
import { useNavigate } from '@/lib/router'

const createUserSchema = z.object({
  username: z.string().trim().min(1, 'Username is required.').max(128, 'Username must be at most 128 characters.').regex(/^\S+$/, 'Username must not contain spaces.'),
  password: z.string().min(8, 'Password must be at least 8 characters.'),
  systemAdmin: z.boolean(),
})

type CreateUserValues = z.infer<typeof createUserSchema>

export default function UserCreatePage() {
  const navigate = useNavigate()
  const createUser = useCreateUser()
  const [submitError, setSubmitError] = useState('')
  const {
    control,
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<CreateUserValues>({
    resolver: zodResolver(createUserSchema),
    defaultValues: { username: '', password: '', systemAdmin: false },
  })

  async function submit(values: CreateUserValues) {
    setSubmitError('')
    try {
      await createUser.mutateAsync({
        username: values.username,
        password: values.password,
        system_admin: values.systemAdmin,
      })
      void navigate('/users')
    } catch (cause) {
      setSubmitError(cause instanceof Error ? cause.message : String(cause))
    }
  }

  return (
    <DataBodyTemplate
      topBar={<PageTopBar left="Users / New User" />}
      title="New User"
      description="Create a local Piper account. Usernames are login identifiers and do not need to be email addresses."
    >
      <DataBodyTemplate.Group
        layout="stacked"
        title="Account"
        description="Set the login credentials and system-wide access."
      >
        <form
          id="create-user-form"
          className="space-y-3"
          noValidate
          onSubmit={handleSubmit(submit)}
        >
          <FormField label="Username" htmlFor="create-user-username" error={errors.username?.message}>
            <Input
              id="create-user-username"
              autoComplete="username"
              placeholder="ml-admin"
              className="h-8 text-sm"
              aria-invalid={!!errors.username}
              {...register('username')}
            />
          </FormField>

          <FormField label="Temporary password" htmlFor="create-user-password" error={errors.password?.message}>
            <Input
              id="create-user-password"
              type="password"
              autoComplete="new-password"
              className="h-8 text-sm"
              aria-invalid={!!errors.password}
              {...register('password')}
            />
          </FormField>

          <Controller
            name="systemAdmin"
            control={control}
            render={({ field }) => (
              <div className="flex items-center justify-between">
                <div>
                  <Label htmlFor="create-user-admin" className="text-sm">System administrator</Label>
                  <p className="text-xs text-muted-foreground">
                    Grants access to system-wide settings and all projects.
                  </p>
                </div>
                <Switch
                  id="create-user-admin"
                  checked={field.value}
                  onCheckedChange={field.onChange}
                />
              </div>
            )}
          />

          <FormActions
            status={submitError || undefined}
            submitLabel={createUser.isPending ? 'Creating…' : 'Create User'}
            submitDisabled={createUser.isPending}
            onCancel={() => void navigate('/users')}
          />
        </form>
      </DataBodyTemplate.Group>
    </DataBodyTemplate>
  )
}
