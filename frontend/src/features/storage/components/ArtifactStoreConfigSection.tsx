import { DataBodyTemplate } from '@loykin/designkit'
import { Badge } from '@/components/ui/badge'
import type { StorageSettingsView } from '@/features/storage/api'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import StatusBadge from '@/shared/components/StatusBadge'
import { warningNotice } from '@/shared/status'
import { BACKEND_LABELS, parseStorageURL } from '@/features/storage/backendUrl'

// This settings surface (backend + credentials) always edits the Piper
// instance actually serving this UI — never a remote federation Member. A
// project owned by a Member has its own separate storage.url, configured on
// that Member's own instance instead. Upload Object / Uploaded Objects below
// are the opposite: they're project-scoped and correctly relay to whichever
// Member owns the current project, so this badge stays on the two groups
// that are actually instance-scoped rather than the whole page.
export function InstanceScopedBadge() {
  return (
    <Badge
      variant="outline"
      className="ml-2 align-middle text-xs font-normal"
      title="Configures only this Piper instance's own storage — not any project owned by a remote federation Member. Check the project switcher's (member) label."
    >
      This instance only
    </Badge>
  )
}


// ── Artifact Store Config ───────────────────────────────────────────────────
// Read-only diagnostic: the artifact storage backend (bucket/endpoint/
// region/which-backend) is deploy-time-only configuration, the same class of
// setting as runtime.type or the database driver — see storage_admin.go's
// StorageSettingsView doc comment on the Go side for the full rationale.
// Every notebook-volume template snapshot, viewer, from_artifact/run:latest
// resolution, and past run's artifact download that references the current
// backend would go permanently unreachable the moment a live-edited backend
// took effect, with no warning — so this section only ever shows what's
// actually running and what's pending on disk, never an editable form.
// Changing the backend requires editing storage.yaml directly and
// restarting the server. This is the "top = what's actually running
// (read-only)" half of the page; System Credentials below is the "bottom =
// manage stored credential entries (editable)" half.

interface ArtifactStoreConfigSectionProps {
  storage: StorageSettingsView | null
  isLoading: boolean
  loadError: unknown
  onRetry: () => void
}

export function ArtifactStoreConfigSection({ storage, isLoading, loadError, onRetry }: ArtifactStoreConfigSectionProps) {
  if (isLoading) {
    return (
      <DataBodyTemplate.Group layout="stacked" title={<>Artifact Store Config<InstanceScopedBadge /></>}>
        <p className="text-sm text-muted-foreground">Loading…</p>
      </DataBodyTemplate.Group>
    )
  }

  if (loadError) {
    return (
      <DataBodyTemplate.Group layout="stacked" title={<>Artifact Store Config<InstanceScopedBadge /></>}>
        <QueryErrorNotice message="Failed to load storage configuration" error={loadError} onRetry={onRetry} />
      </DataBodyTemplate.Group>
    )
  }

  const status = storage?.effective.status ?? 'disabled'
  const backendLabel = storage?.effective.backend || '—'
  const cfg = storage?.config
  const pending = parseStorageURL(cfg?.url ?? '')

  return (
    <DataBodyTemplate.Group
      layout="stacked"
      title={<>Artifact Store Config<InstanceScopedBadge /></>}
      description="Read-only. Changing the artifact storage backend requires editing storage.yaml directly on this server and restarting it — the same as runtime.type or the database driver."
    >
      <DataBodyTemplate.Field label="Runtime Status" description="What's actually active right now.">
        <div className="space-y-1 text-sm">
          <p><span className="text-muted-foreground">Status: </span><StatusBadge status={status} /></p>
          <p><span className="text-muted-foreground">Backend: </span>{backendLabel}</p>
          <p><span className="text-muted-foreground">Reason: </span>{storage?.effective.reason || '—'}</p>
        </div>
      </DataBodyTemplate.Field>

      <DataBodyTemplate.Field label="Config File" description="Read from this path on startup.">
        <span className="break-all font-mono text-xs">{storage?.config_path || '—'}</span>
      </DataBodyTemplate.Field>

      <DataBodyTemplate.Field
        label="Pending Config"
        description={storage?.restart_required
          ? 'storage.yaml differs from the running configuration — restart the server to apply it.'
          : 'What storage.yaml currently holds. Matches the running configuration.'}
      >
        <div className="space-y-1 text-sm">
          <p><span className="text-muted-foreground">Enabled: </span>{cfg?.disabled ? 'No' : 'Yes'}</p>
          <p><span className="text-muted-foreground">Backend: </span>{BACKEND_LABELS[pending.backend]}</p>
          {pending.backend === 's3' && (
            <>
              <p><span className="text-muted-foreground">Bucket: </span>{pending.bucket || '—'}</p>
              <p><span className="text-muted-foreground">Endpoint: </span>{pending.endpoint || '(AWS S3)'}</p>
              <p><span className="text-muted-foreground">Region: </span>{pending.region || '—'}</p>
              <p><span className="text-muted-foreground">Force path style: </span>{pending.forcePathStyle ? 'Yes' : 'No'}</p>
            </>
          )}
          {(pending.backend === 'gcs' || pending.backend === 'azure') && (
            <p><span className="text-muted-foreground">{pending.backend === 'gcs' ? 'Bucket' : 'Container'}: </span>{pending.bucket || '—'}</p>
          )}
          {pending.backend === 'http' && (
            <>
              <p><span className="text-muted-foreground">Base URL: </span>{pending.httpURL || '—'}</p>
              <p><span className="text-muted-foreground">Bearer token: </span>{cfg?.token ? 'set' : 'not set'}</p>
            </>
          )}
          <p><span className="text-muted-foreground">Credential: </span>{cfg?.credentialRef || 'None'}</p>
        </div>
      </DataBodyTemplate.Field>

      {storage?.restart_required && (
        <p className={`p-3 text-xs ${warningNotice}`}>Restart required to apply storage.yaml.</p>
      )}
    </DataBodyTemplate.Group>
  )
}
