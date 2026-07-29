import { useEffect, useState } from 'react'
import { LoginBodyTemplate } from '@loykin/designkit'
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm } from 'react-hook-form'
import { z } from 'zod'
import { useNavigate } from '@/lib/router'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { login, bootstrap, bootstrapStatus } from '@/features/auth/api'
import { useAuth } from '@/features/auth/context'

const loginSchema = z.object({
  username: z.string().trim().min(1, 'Username is required.').max(128, 'Username must be at most 128 characters.').regex(/^\S+$/, 'Username must not contain spaces.'),
  password: z.string().min(1, 'Password is required.'),
})

type LoginValues = z.infer<typeof loginSchema>

export default function LoginPage() {
  const navigate = useNavigate()
  const { capabilities, loading: authLoading, setUser } = useAuth()
  const [submitError, setSubmitError] = useState('')
  // undefined while we haven't checked yet — avoids flashing the sign-in
  // form before we know whether this is a fresh install with zero users.
  const [needsBootstrap, setNeedsBootstrap] = useState<boolean | undefined>(undefined)
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<LoginValues>({
    resolver: zodResolver(loginSchema),
    defaultValues: { username: '', password: '' },
  })

  useEffect(() => {
    if (!authLoading && capabilities && !capabilities.authentication) {
      navigate('/', { replace: true })
    }
  }, [authLoading, capabilities, navigate])

  useEffect(() => {
    if (authLoading || !capabilities?.authentication || capabilities.login_mode !== 'password') return
    bootstrapStatus()
      .then(s => setNeedsBootstrap(s.required))
      .catch(() => setNeedsBootstrap(false))
  }, [authLoading, capabilities])

  if (authLoading || needsBootstrap === undefined) return null

  if (capabilities?.login_mode === 'redirect') {
    return (
      <LoginBodyTemplate
        layout="centered"
        card="card"
        cardWidth="sm"
        brand={<div className="flex h-10 w-10 items-center justify-center rounded bg-primary text-sm font-bold text-primary-foreground">P</div>}
      >
        <Button onClick={() => window.location.assign(capabilities.login_url)}>
          Continue with SSO
        </Button>
      </LoginBodyTemplate>
    )
  }

  if (capabilities?.login_mode !== 'password') {
    return (
      <LoginBodyTemplate layout="centered" card="card" cardWidth="sm">
        <p className="text-sm text-muted-foreground">Authentication is managed by the host application.</p>
      </LoginBodyTemplate>
    )
  }

  async function submit(values: LoginValues) {
    setSubmitError('')
    try {
      if (needsBootstrap) {
        await bootstrap(values)
        // Bootstrap only creates the account; sign in with the same
        // credentials to establish a session, same as any other login.
      }
      const user = await login(values)
      setUser(user)
      navigate('/', { replace: true })
    } catch (err: unknown) {
      setSubmitError(err instanceof Error ? err.message : (needsBootstrap ? 'Setup failed' : 'Login failed'))
    }
  }

  return (
    <LoginBodyTemplate
      layout="centered"
      card="card"
      cardWidth="sm"
      brand={<div className="flex h-10 w-10 items-center justify-center rounded bg-primary text-sm font-bold text-primary-foreground">P</div>}
    >
      <div className="space-y-6">
        <div className="space-y-1 text-center">
          <h1 className="text-xl font-semibold">
            {needsBootstrap ? 'Create the admin account' : 'Sign in to piper'}
          </h1>
          {needsBootstrap && (
            <p className="text-sm text-muted-foreground">
              No users exist yet. Set up the first account — it will have
              system admin access.
            </p>
          )}
        </div>

        <form onSubmit={handleSubmit(submit)} className="space-y-4" noValidate>
          <div className="space-y-1.5">
            <Label htmlFor="username">Username</Label>
            <Input
              id="username"
              autoComplete="username"
              placeholder="admin"
              aria-invalid={!!errors.username}
              {...register('username')}
            />
            {errors.username && <p className="text-xs text-destructive">{errors.username.message}</p>}
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="password">Password</Label>
            <Input
              id="password"
              type="password"
              autoComplete={needsBootstrap ? 'new-password' : 'current-password'}
              aria-invalid={!!errors.password}
              {...register('password')}
            />
            {errors.password && <p className="text-xs text-destructive">{errors.password.message}</p>}
          </div>
          {submitError && (
            <p className="text-sm text-destructive" role="alert">{submitError}</p>
          )}
          <Button type="submit" className="w-full" disabled={isSubmitting}>
            {isSubmitting
              ? (needsBootstrap ? 'Creating account…' : 'Signing in…')
              : (needsBootstrap ? 'Create admin account' : 'Sign in')}
          </Button>
        </form>
      </div>
    </LoginBodyTemplate>
  )
}
