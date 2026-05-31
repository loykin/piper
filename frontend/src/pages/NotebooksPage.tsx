import { useEffect, useMemo, useRef, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { DataGrid, DataGridPaginationCompact } from '@loykin/gridkit'
import { DataPage } from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import {
  listNotebooks, stopNotebook, startNotebook, deleteNotebook,
  listNotebookVolumes,
  type NotebookServer, type NotebookVolume,
} from '@/features/notebooks/api'
import { getNotebookColumns } from '@/features/notebooks/columns'
import LaunchPanel from '@/features/notebooks/components/LaunchPanel'

export default function NotebooksPage() {
  const location = useLocation()
  const attachVolumeId = (location.state as { attachVolumeId?: string } | null)?.attachVolumeId
  const [notebooks, setNotebooks] = useState<NotebookServer[]>([])
  const [releasedVolumes, setReleasedVolumes] = useState<NotebookVolume[]>([])
  const [loading, setLoading] = useState(true)
  const [showLaunch, setShowLaunch] = useState(!!attachVolumeId)
  const [initialVolumeId, setInitialVolumeId] = useState<string | undefined>(attachVolumeId)
  const [busy, setBusy] = useState<string | null>(null)
  const intervalRef = useRef<ReturnType<typeof setInterval>>(0 as unknown as ReturnType<typeof setInterval>)

  const load = () =>
    Promise.all([
      listNotebooks().catch(() => [] as NotebookServer[]),
      listNotebookVolumes().catch(() => [] as NotebookVolume[]),
    ])
      .then(([nbs, vols]) => {
        setNotebooks(nbs)
        setReleasedVolumes(vols.filter(v => v.status === 'released'))
      })
      .finally(() => setLoading(false))

  useEffect(() => {
    void load()
    intervalRef.current = setInterval(() => void load(), 5000)
    return () => clearInterval(intervalRef.current)
  }, [])

  const withBusy = async (key: string, fn: () => Promise<void>) => {
    setBusy(key)
    try {
      await fn()
      await load()
    } catch (err) {
      alert(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(null)
    }
  }

  const handleStop   = (name: string) => withBusy(name, () => stopNotebook(name))
  const handleStart  = (name: string) => withBusy(name, () => startNotebook(name).then(() => {}))
  const handleDelete = (name: string) => {
    if (!confirm(`Delete notebook "${name}"?\nThe volume and work directory are preserved. You can recover them from the Volumes page.`)) return
    void withBusy(name, () => deleteNotebook(name))
  }

  // Open the launch panel with a specific volume pre-selected.
  const openLaunch = (volId?: string) => {
    setInitialVolumeId(volId)
    setShowLaunch(true)
  }
  const closeLaunch = () => {
    setShowLaunch(false)
    setInitialVolumeId(undefined)
  }

  const columns = useMemo(
    () => getNotebookColumns(busy, handleStop, handleStart, handleDelete),
    [busy],
  )

  return (
    <DataPage>
      <DataPage.Header>
        <DataPage.TitleBlock
          title="Notebooks"
          description="Jupyter notebook servers. Click Open to launch in a new tab."
        />
        {!showLaunch && (
          <DataPage.Actions>
            <Button size="sm" onClick={() => openLaunch()}>Launch</Button>
          </DataPage.Actions>
        )}
      </DataPage.Header>

      <DataPage.Content>
        {showLaunch && (
          <LaunchPanel
            onClose={closeLaunch}
            onLaunched={() => void load()}
            initialVolumeId={initialVolumeId}
            releasedVolumes={releasedVolumes}
          />
        )}

        {loading ? (
          <div className="py-8 text-sm text-muted-foreground">Loading…</div>
        ) : notebooks.length === 0 ? (
          <div className="py-12 text-center">
            <p className="text-sm text-muted-foreground">No notebook servers running.</p>
            {releasedVolumes.length > 0 && (
              <p className="mt-1 text-xs text-muted-foreground/60">
                {releasedVolumes.length} released volume{releasedVolumes.length > 1 ? 's' : ''} available — click Launch to attach one.
              </p>
            )}
          </div>
        ) : (
          <DataPage.Group surface="none" className="h-full">
            <DataPage.GroupBody className="h-full [&_.dg-shell]:h-full [&_.dg-table-wrapper]:min-h-0 [&_.dg-table-wrapper]:flex-1">
              <DataGrid
                data={notebooks}
                columns={columns}
                tableWidthMode="fill-last"
                rowHeight={44}
                pagination={{ pageSize: 20 }}
                footer={(table) => (
                  <div className="flex h-9 items-center justify-between px-1 text-xs text-muted-foreground">
                    <span>{notebooks.length} servers</span>
                    {releasedVolumes.length > 0 && (
                      <button
                        type="button"
                        className="text-xs text-primary hover:underline"
                        onClick={() => openLaunch(releasedVolumes[0].id)}
                      >
                        {releasedVolumes.length} released volume{releasedVolumes.length > 1 ? 's' : ''} — Attach
                      </button>
                    )}
                    <DataGridPaginationCompact table={table} />
                  </div>
                )}
              />
            </DataPage.GroupBody>
          </DataPage.Group>
        )}
      </DataPage.Content>
    </DataPage>
  )
}
