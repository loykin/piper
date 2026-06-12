/**
 * Central HTTP client for the Piper API.
 *
 * All project-scoped requests go through `projectApi(projectId)`.
 * System-scoped requests (workers, storage settings) use `api` directly.
 */

export class ApiError extends Error {
  readonly status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

async function parseError(res: Response): Promise<string> {
  const body = await res.text().catch(() => '')
  try {
    const obj = JSON.parse(body) as { error?: string }
    return obj.error ?? `${res.status} ${res.statusText}`
  } catch {
    return body || `${res.status} ${res.statusText}`
  }
}

let _refreshPromise: Promise<boolean> | null = null
let _refreshEnabled = false

export function configureAuthRefresh(enabled: boolean) {
  _refreshEnabled = enabled
}

async function tryRefresh(): Promise<boolean> {
  if (_refreshPromise) return _refreshPromise
  _refreshPromise = fetch('/api/auth/refresh', { method: 'POST', credentials: 'include' })
    .then(r => r.ok)
    .catch(() => false)
    .finally(() => { _refreshPromise = null })
  return _refreshPromise
}

async function request<T = unknown>(
  url: string,
  init?: RequestInit,
  retried = false,
): Promise<T> {
  const res = await fetch(url, {
    headers: {
      'Content-Type': 'application/json',
      ...(init?.headers ?? {}),
    },
    credentials: 'include',
    ...init,
  })
  if (res.status === 401 && !retried && _refreshEnabled) {
    const ok = await tryRefresh()
    if (ok) return request<T>(url, init, true)
  }
  if (!res.ok) {
    throw new ApiError(res.status, await parseError(res))
  }
  const text = await res.text()
  return text ? (JSON.parse(text) as T) : (undefined as T)
}

async function upload<T = unknown>(url: string, form: FormData): Promise<T> {
  const res = await fetch(url, { method: 'POST', body: form })
  if (!res.ok) {
    throw new ApiError(res.status, await parseError(res))
  }
  return res.json() as Promise<T>
}

/** Fetch helper that returns raw Response (for streaming / SSE). */
async function requestRaw(url: string, init?: RequestInit): Promise<Response> {
  return fetch(url, init)
}

// ── system-scoped endpoints (no project prefix) ──────────────────────────────

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: 'POST', body: body !== undefined ? JSON.stringify(body) : undefined }),
  put: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: 'PUT', body: body !== undefined ? JSON.stringify(body) : undefined }),
  patch: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: 'PATCH', body: body !== undefined ? JSON.stringify(body) : undefined }),
  delete: (path: string) => request<void>(path, { method: 'DELETE' }),
  upload: <T>(path: string, form: FormData) => upload<T>(path, form),
  raw: (path: string, init?: RequestInit) => requestRaw(path, init),
}

// ── project-scoped endpoints ──────────────────────────────────────────────────

export function projectApi(projectId: string) {
  const base = `/api/projects/${encodeURIComponent(projectId)}`
  return {
    get: <T>(path: string) => request<T>(`${base}${path}`),
    post: <T>(path: string, body?: unknown) =>
      request<T>(`${base}${path}`, {
        method: 'POST',
        body: body !== undefined ? JSON.stringify(body) : undefined,
      }),
    put: <T>(path: string, body?: unknown) =>
      request<T>(`${base}${path}`, {
        method: 'PUT',
        body: body !== undefined ? JSON.stringify(body) : undefined,
      }),
    patch: <T>(path: string, body?: unknown) =>
      request<T>(`${base}${path}`, {
        method: 'PATCH',
        body: body !== undefined ? JSON.stringify(body) : undefined,
      }),
    delete: (path: string) => request<void>(`${base}${path}`, { method: 'DELETE' }),
    upload: <T>(path: string, form: FormData) => upload<T>(`${base}${path}`, form),
    /** Browser proxy URL (not /api prefix) */
    proxyBase: `/projects/${encodeURIComponent(projectId)}`,
  }
}
