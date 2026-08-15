import { useMemo, useState } from 'react'
import { useNavigate } from '@/lib/router'
import { DataGrid, DataGridPaginationCompact } from '@loykin/gridkit'
import { DataBodyTemplate } from '@loykin/designkit'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { getNotebookVolumeColumns } from '@/features/notebooks/columns'
import { useNotebookVolumes, usePurgeVolume } from '@/features/notebooks/hooks'
import type { NotebookVolume } from '@/features/notebooks/api'

export default function NotebookVolumesPage() {
  const navigate = useNavigate()
  const { data: volumes = [] } = useNotebookVolumes()
  const { mutate: purgeVolume, isPending: purging, variables: purgingId } = usePurgeVolume()
  const [purgeTarget, setPurgeTarget] = useState<NotebookVolume | null>(null)

  const busy = purging ? (purgingId ?? null) : null

  const handlePurge = (vol: NotebookVolume) => setPurgeTarget(vol)

  const handleAttach = (volId: string) => navigate(`/notebooks/create?volume=${volId}`)

  const columns = useMemo(
    () => getNotebookVolumeColumns(busy, handleAttach, handlePurge),
    [busy],
  )

  return (
    <>
    <DataBodyTemplate
      title="Notebook Volumes"
      description="Persistent storage for notebook servers. Volumes survive server deletion."
    >
      <DataBodyTemplate.Body>
        <DataBodyTemplate.Resource>
          <DataGrid
            data={volumes}
            columns={columns}
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
        </DataBodyTemplate.Resource>
      </DataBodyTemplate.Body>
    </DataBodyTemplate>

    <AlertDialog open={purgeTarget != null} onOpenChange={open => { if (!open) setPurgeTarget(null) }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Purge this volume?</AlertDialogTitle>
          <AlertDialogDescription>
            "{purgeTarget?.label}" will permanently delete {purgeTarget?.work_dir} and all its files. This cannot be undone.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={purging}
            onClick={() => {
              if (!purgeTarget) return
              purgeVolume(purgeTarget.id)
              setPurgeTarget(null)
            }}
          >
            {purging ? 'Purging…' : 'Purge volume'}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
    </>
  )
}
