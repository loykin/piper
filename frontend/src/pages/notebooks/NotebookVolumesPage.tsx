import { useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { DataGrid, DataGridPaginationCompact } from '@loykin/gridkit'
import { DataBodyTemplate } from '@loykin/designkit'
import { getNotebookVolumeColumns } from '@/features/notebooks/columns'
import { useNotebookVolumes, useNotebookWorkers, usePurgeVolume } from '@/features/notebooks/hooks'
import type { NotebookVolume } from '@/features/notebooks/api'

export default function NotebookVolumesPage() {
  const navigate = useNavigate()
  const { data: volumes = [], isLoading } = useNotebookVolumes()
  const { data: workers = [] } = useNotebookWorkers()
  const { mutate: purgeVolume, isPending: purging, variables: purgingId } = usePurgeVolume()

  // Resolves legacy UUID worker_id → hostname; new volumes store hostname directly.
  const workerIdMap = useMemo(() => {
    const m: Record<string, string> = {}
    for (const w of workers) m[w.id] = w.hostname || w.id
    return m
  }, [workers])

  const busy = purging ? (purgingId ?? null) : null

  const handlePurge = (vol: NotebookVolume) => {
    if (!confirm(`Purge volume "${vol.label}"?\nThis will permanently delete ${vol.work_dir} and all its files. This cannot be undone.`)) return
    purgeVolume(vol.id)
  }

  const handleAttach = (volId: string) => navigate(`/notebooks/create?volume=${volId}`)

  const columns = useMemo(
    () => getNotebookVolumeColumns(busy, workerIdMap, handleAttach, handlePurge),
    [busy, workerIdMap],
  )

  return (
    <DataBodyTemplate
      title="Notebook Volumes"
      description="Persistent storage for notebook servers. Volumes survive server deletion."
    >
      <DataBodyTemplate.Body>
        <DataGrid
          data={volumes}
          columns={columns}
          isLoading={isLoading}
          emptyContent={
            <div className="py-12 text-center">
              <p className="text-sm text-muted-foreground">No volumes yet.</p>
              <p className="mt-1 text-xs text-muted-foreground/60">
                Volumes are created automatically when you launch a notebook server.
              </p>
            </div>
          }
          tableWidthMode="fill-last"
          rowHeight={44}
          pagination={{ pageSize: 20 }}
          footer={(table) => (
            <div className="flex h-9 items-center justify-between px-1 text-xs text-muted-foreground">
              <span>{volumes.length} volumes</span>
              <DataGridPaginationCompact table={table} />
            </div>
          )}
        />
      </DataBodyTemplate.Body>
    </DataBodyTemplate>
  )
}
