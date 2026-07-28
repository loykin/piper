import { useEffect, useState } from 'react'
import { useNavigate } from '@/lib/router'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { login, bootstrap, bootstrapStatus } from '@/features/auth/api'
import { useAuth } from '@/features/auth/context'

export default function LoginPage() {
  const navigate = useNavigate()
  const { capabilities, loading: authLoading, setUser } = useAuth()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  // undefined while we haven't checked yet — avoids flashing the sign-in
  // form before we know whether this is a fresh install with zero users.
  const [needsBootstrap, setNeedsBootstrap] = useState<boolean | undefined>(undefined)

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
      <div className="flex h-screen items-center justify-center bg-background">
        <Button onClick={() => window.location.assign(capabilities.login_url)}>
          Continue with SSO
        </Button>
      </div>
    )
  }

  if (capabilities?.login_mode !== 'password') {
    return (
      <div className="flex h-screen items-center justify-center bg-background text-sm text-muted-foreground">
        Authentication is managed by the host application.
      </div>
    )
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      if (needsBootstrap) {
        await bootstrap({ email, password })
        // Bootstrap only creates the account; sign in with the same
        // credentials to establish a session, same as any other login.
      }
      const user = await login({ email, password })
      setUser(user)
      navigate('/', { replace: true })
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : (needsBootstrap ? 'Setup failed' : 'Login failed'))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="flex h-screen items-center justify-center bg-background">
      <div className="w-full max-w-sm space-y-6 rounded-lg border bg-card p-8 shadow-sm">
        <div className="space-y-1 text-center">
          <div className="flex justify-center">
            <div className="flex h-10 w-10 items-center justify-center rounded bg-primary text-sm font-bold text-primary-foreground">
              P
            </div>
          </div>
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

        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="email">Email</Label>
            <Input
              id="email"
              type="email"
              autoComplete="email"
              placeholder="you@example.com"
              value={email}
              onChange={e => setEmail(e.target.value)}
              required
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="password">Password</Label>
            <Input
              id="password"
              type="password"
              autoComplete={needsBootstrap ? 'new-password' : 'current-password'}
              value={password}
              onChange={e => setPassword(e.target.value)}
              required
            />
          </div>
          {error && (
            <p className="text-sm text-destructive">{error}</p>
          )}
          <Button type="submit" className="w-full" disabled={loading}>
            {loading
              ? (needsBootstrap ? 'Creating account…' : 'Signing in…')
              : (needsBootstrap ? 'Create admin account' : 'Sign in')}
          </Button>
        </form>
      </div>
    </div>
  )
}
