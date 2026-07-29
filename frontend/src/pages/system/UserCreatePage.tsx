import { useState } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import {
  Button,
  DataBodyTemplate,
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
          <div className="space-y-1.5">
            <Label htmlFor="create-user-username" className="text-xs">Username</Label>
            <Input
              id="create-user-username"
              autoComplete="username"
              placeholder="ml-admin"
              className="h-8 text-sm"
              aria-invalid={!!errors.username}
              {...register('username')}
            />
            {errors.username && <p className="text-xs text-destructive">{errors.username.message}</p>}
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="create-user-password" className="text-xs">Temporary password</Label>
            <Input
              id="create-user-password"
              type="password"
              autoComplete="new-password"
              className="h-8 text-sm"
              aria-invalid={!!errors.password}
              {...register('password')}
            />
            {errors.password && <p className="text-xs text-destructive">{errors.password.message}</p>}
          </div>

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

          {submitError && <p className="text-sm text-destructive" role="alert">{submitError}</p>}
          <div className="flex justify-end gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-8 text-xs"
              onClick={() => void navigate('/users')}
            >
              Cancel
            </Button>
            <Button type="submit" size="sm" className="h-8 text-xs" disabled={createUser.isPending}>
              {createUser.isPending ? 'Creating…' : 'Create User'}
            </Button>
          </div>
        </form>
      </DataBodyTemplate.Group>
    </DataBodyTemplate>
  )
}
