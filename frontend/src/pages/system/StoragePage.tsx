import { useProjectId } from '@/features/projects/context'
import { useSearchParams } from '@/lib/router'
import { DataBodyTemplate, PageTopBar } from '@loykin/designkit'
import { SidePanelProvider } from '@loykin/side-panel'
import { Badge } from '@/components/ui/badge'
import { useSystemCredentials } from '@/features/credentials/hooks'
import { useStorageSettings } from '@/features/storage/hooks'
import { BACKEND_CREDENTIAL_KIND, parseStorageURL } from '@/features/storage/backendUrl'
import { ArtifactStoreConfigSection } from '@/features/storage/components/ArtifactStoreConfigSection'
import { statusVariant } from '@/features/storage/status'
import { StorageCredentialsSection } from '@/features/storage/components/SystemCredentialsSection'
import { UploadedObjectsSection } from '@/features/storage/components/UploadedObjectsSection'
import { PageCrumbs } from '@/shared/components/PageCrumbs'

// ── route component ─────────────────────────────────────────────────────────
// Owns only the page template, breadcrumb/title, and the state genuinely
// shared across the two Configuration-tab sections: which backend is
// pending in storage.yaml and which credential it names. Both are derived
// read-only from the settings query now — Artifact Store Config no longer
// has editable state to own, and System Credentials only reads this to
// filter its list and label the "in use" credential.

const DEFAULT_TAB = 'objects'

function StoragePageInner() {
  const projectId = useProjectId()
  const settingsQuery = useStorageSettings()
  const storage = settingsQuery.data ?? null

  const [searchParams, setSearchParams] = useSearchParams()
  const activeTab = searchParams.get('tab') ?? DEFAULT_TAB

  function handleTabChange(next: string) {
    setSearchParams({ ...Object.fromEntries(searchParams), tab: next }, { replace: true })
  }

  const backend = parseStorageURL(storage?.config.url ?? '').backend
  const credentialRef = storage?.config.credentialRef ?? ''

  const { data: systemCredentials = [] } = useSystemCredentials()
  const activeCredentialKind = BACKEND_CREDENTIAL_KIND[backend]
  // Keep storage.credentialRef's current credential listed even if it has
  // since been disabled, so a disabled-but-in-use credential doesn't just
  // vanish from this list — the "in use" badge below still needs to find it.
  const backendCredentials = systemCredentials.filter(c => c.kind === activeCredentialKind && (!c.disabled || c.name === credentialRef))

  const status = storage?.effective.status ?? 'disabled'
  const restartRequired = storage?.restart_required ?? false

  return (
    <DataBodyTemplate topBar={<PageTopBar left={<PageCrumbs items={['Infrastructure', 'Storage']} />} />}
      title="Storage"
      activeTab={activeTab}
      onTabChange={handleTabChange}
      status={
        settingsQuery.isSuccess && (
          <>
            <Badge variant={statusVariant(status)}>{status}</Badge>
            {restartRequired && <Badge variant="outline">Restart required</Badge>}
          </>
        )
      }
    >
      <DataBodyTemplate.Tab id="objects" label="Objects">
        <UploadedObjectsSection projectId={projectId} />
      </DataBodyTemplate.Tab>

      <DataBodyTemplate.Tab id="config" label="Configuration">
        <ArtifactStoreConfigSection
          storage={storage}
          isLoading={settingsQuery.isPending}
          loadError={settingsQuery.error}
          onRetry={() => void settingsQuery.refetch()}
        />

        {activeCredentialKind && (
          <StorageCredentialsSection
            backend={backend}
            activeCredentialKind={activeCredentialKind}
            backendCredentials={backendCredentials}
            credentialRef={credentialRef}
          />
        )}
      </DataBodyTemplate.Tab>
    </DataBodyTemplate>
  )
}

export default function StoragePage() {
  return (
    <SidePanelProvider defaultSize={480} defaultMinSize={380} defaultMaxSize={800}>
      <StoragePageInner />
    </SidePanelProvider>
  )
}
