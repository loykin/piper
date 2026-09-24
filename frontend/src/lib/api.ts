/**
 * Central HTTP client for the Piper API.
 *
 * All project-scoped requests go through `projectApi(projectId)`.
 * System-scoped requests (system settings, users) use `api` directly.
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

async function fetchOk(url: string, init?: RequestInit, retried = false): Promise<Response> {
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
    if (ok) return fetchOk(url, init, true)
  }
  if (!res.ok) {
    throw new ApiError(res.status, await parseError(res))
  }
  return res
}

async function request<T = unknown>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetchOk(url, init)
  const text = await res.text()
  return text ? (JSON.parse(text) as T) : (undefined as T)
}

/** One page of a paginated list endpoint. */
export type Paged<T> = { items: T[]; total: number }

/**
 * Thrown when a response doesn't match the list contract — not an array, or
 * a paginated response missing `X-Total-Count`. Surfacing this as an error
 * (instead of coercing it to an empty list) keeps a backend regression from
 * masquerading as "nothing here yet" on a list page.
 */
export class ContractError extends ApiError {
  constructor(url: string, detail: string) {
    super(0, `Unexpected response from ${url}: ${detail}`)
    this.name = 'ContractError'
  }
}

async function readJSON(res: Response): Promise<unknown> {
  const text = await res.text()
  return text ? JSON.parse(text) : undefined
}

async function requestList<T>(url: string): Promise<T[]> {
  const data = await readJSON(await fetchOk(url))
  if (!Array.isArray(data)) throw new ContractError(url, 'expected a JSON array')
  return data as T[]
}

/**
 * GET a `limit`/`offset` paginated list. The server reports the total row
 * count (ignoring limit/offset) in `X-Total-Count` whenever `limit` is sent.
 */
async function requestPaged<T>(url: string): Promise<Paged<T>> {
  const res = await fetchOk(url)
  const header = res.headers.get('X-Total-Count')
  const data = await readJSON(res)
  if (!Array.isArray(data)) throw new ContractError(url, 'expected a JSON array')
  const total = header === null ? NaN : Number(header)
  if (!Number.isFinite(total)) throw new ContractError(url, 'missing X-Total-Count header')
  return { items: data as T[], total }
}

/** GET a cursor-paginated list; the next cursor comes from `X-Next-Cursor`. */
async function requestCursorList<T>(url: string): Promise<{ items: T[]; nextCursor: string | null }> {
  const res = await fetchOk(url)
  const data = await readJSON(res)
  if (!Array.isArray(data)) throw new ContractError(url, 'expected a JSON array')
  return { items: data as T[], nextCursor: res.headers.get('X-Next-Cursor') }
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
  getList: <T>(path: string) => requestList<T>(path),
  getPaged: <T>(path: string) => requestPaged<T>(path),
  getCursorList: <T>(path: string) => requestCursorList<T>(path),
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
    getList: <T>(path: string) => requestList<T>(`${base}${path}`),
    getPaged: <T>(path: string) => requestPaged<T>(`${base}${path}`),
    getCursorList: <T>(path: string) => requestCursorList<T>(`${base}${path}`),
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
