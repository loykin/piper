// storage feature — backend-type <-> storage.url conversion.
//
// storage.url stays a single opaque string on the wire (see StorageConfig),
// but hand-writing "s3://bucket?endpoint=...&s3ForcePathStyle=true" is not
// something a UI should ask an operator to do. These helpers let the page
// work in terms of a backend type and its own structured fields, and
// compose/parse the URL at the edges.

export type StorageBackendType = 'file' | 's3' | 'gcs' | 'azure' | 'http'

export interface BackendForm {
  backend: StorageBackendType
  bucket: string // s3 bucket / gcs bucket / azure container
  endpoint: string // s3 only — e.g. http://localhost:9000
  region: string // s3 only
  forcePathStyle: boolean // s3 only
  httpURL: string // http/https only — the full base URL
}

export function emptyBackendForm(): BackendForm {
  return { backend: 'file', bucket: '', endpoint: '', region: '', forcePathStyle: true, httpURL: '' }
}

export function parseStorageURL(url: string): BackendForm {
  const empty = emptyBackendForm()
  const trimmed = url.trim()
  if (!trimmed) return empty
  let u: URL
  try {
    u = new URL(trimmed)
  } catch {
    return empty
  }
  switch (u.protocol) {
    case 'file:':
      return empty
    case 's3:':
      return {
        ...empty,
        backend: 's3',
        bucket: u.hostname,
        endpoint: u.searchParams.get('endpoint') ?? '',
        region: u.searchParams.get('region') ?? '',
        forcePathStyle: u.searchParams.get('s3ForcePathStyle') !== 'false',
      }
    case 'gs:':
      return { ...empty, backend: 'gcs', bucket: u.hostname }
    case 'azblob:':
      return { ...empty, backend: 'azure', bucket: u.hostname }
    case 'http:':
    case 'https:':
      return { ...empty, backend: 'http', httpURL: trimmed }
    default:
      return empty
  }
}

export function composeStorageURL(form: BackendForm): string {
  switch (form.backend) {
    case 'file':
      return ''
    case 's3': {
      if (!form.bucket.trim()) return ''
      const params = new URLSearchParams()
      if (form.region.trim()) params.set('region', form.region.trim())
      if (form.endpoint.trim()) params.set('endpoint', form.endpoint.trim())
      params.set('s3ForcePathStyle', form.forcePathStyle ? 'true' : 'false')
      return `s3://${form.bucket.trim()}?${params.toString()}`
    }
    case 'gcs':
      return form.bucket.trim() ? `gs://${form.bucket.trim()}` : ''
    case 'azure':
      return form.bucket.trim() ? `azblob://${form.bucket.trim()}` : ''
    case 'http':
      return form.httpURL.trim()
  }
}

export const BACKEND_LABELS: Record<StorageBackendType, string> = {
  file: 'File (built-in)',
  s3: 'S3-compatible',
  gcs: 'Google Cloud Storage',
  azure: 'Azure Blob Storage',
  http: 'HTTP(S)',
}

// Credential kind each backend resolves storage.credentialRef against.
// file/http don't use system credentials for storage access.
export const BACKEND_CREDENTIAL_KIND: Partial<Record<StorageBackendType, 's3' | 'gcs' | 'azure'>> = {
  s3: 's3',
  gcs: 'gcs',
  azure: 'azure',
}
