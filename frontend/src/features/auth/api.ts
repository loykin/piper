export interface AuthUser {
  id: string
  email: string
  display_name?: string
  system_admin: boolean
  disabled?: boolean
}

export interface AuthCapabilities {
  authentication: boolean
  login_routes: boolean
  login_mode: '' | 'password' | 'redirect'
  login_url: string
  user_directory: boolean
  user_management: boolean
  project_member_management: boolean
}

export interface LoginRequest {
  email: string
  password: string
}

export async function capabilities(): Promise<AuthCapabilities> {
  const res = await fetch('/api/capabilities', { credentials: 'include' })
  return handleResponse<AuthCapabilities>(res)
}

async function handleResponse<T>(res: Response): Promise<T> {
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    try {
      const obj = JSON.parse(body) as { error?: string }
      throw new Error(obj.error ?? `${res.status} ${res.statusText}`)
    } catch {
      throw new Error(body || `${res.status} ${res.statusText}`)
    }
  }
  const text = await res.text()
  return text ? (JSON.parse(text) as T) : (undefined as T)
}

export async function login(req: LoginRequest): Promise<AuthUser> {
  const res = await fetch('/api/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
    credentials: 'include',
  })
  const data = await handleResponse<{ user: AuthUser }>(res)
  return data.user
}

export async function logout(): Promise<void> {
  await fetch('/api/auth/logout', { method: 'POST', credentials: 'include' })
}

export async function refresh(): Promise<AuthUser | null> {
  const res = await fetch('/api/auth/refresh', {
    method: 'POST',
    credentials: 'include',
  })
  if (!res.ok) return null
  const data = await res.json() as { user: AuthUser }
  return data.user
}

export async function me(): Promise<AuthUser | null> {
  const res = await fetch('/api/auth/me', { credentials: 'include' })
  if (!res.ok) return null
  return res.json() as Promise<AuthUser>
}
