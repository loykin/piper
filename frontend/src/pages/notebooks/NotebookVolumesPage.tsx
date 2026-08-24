import { useMemo, useState } from 'react'
import { Search } from 'lucide-react'
import { useNavigate } from '@/lib/router'
import { DataGrid, DataGridPaginationBar } from '@loykin/gridkit'
import { DataBodyTemplate } from '@loykin/designkit'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { FilterInput } from '@loykin/filter-input'
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
import { useNotebookVolumesPaged, usePurgeVolume } from '@/features/notebooks/hooks'
import type { NotebookVolume } from '@/features/notebooks/api'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { NotebookVolumeDetailPanel } from '@/features/notebooks/components/NotebookVolumeDetailPanel'

const PAGE_SIZE = 20

function NotebookVolumesPageInner() {
  const { open } = useSidePanel()
  const navigate = useNavigate()
  const [pageIndex, setPageIndex] = useState(0)
  const volumesQuery = useNotebookVolumesPaged(PAGE_SIZE, pageIndex * PAGE_SIZE)
  const total = volumesQuery.data?.total ?? 0
  const { mutate: purgeVolume, isPending: purging, variables: purgingId } = usePurgeVolume()
  const [purgeTarget, setPurgeTarget] = useState<NotebookVolume | null>(null)
  const [labelFilter, setLabelFilter] = useState('')
  // Filters only the current page — not server-side yet, same accepted
  // trade-off as CredentialsPage's kind filter.
  const filteredVolumes = useMemo(() => {
    const list = volumesQuery.data?.volumes ?? []
    if (!labelFilter.trim()) return list
    const q = labelFilter.trim().toLowerCase()
    return list.filter(v => v.label.toLowerCase().includes(q))
  }, [volumesQuery.data, labelFilter])

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
        <DataBodyTemplate.Resource
          toolbarLeft={
            <div className="w-48">
              <FilterInput
                config={{
                  key: 'volumeSearch',
                  type: 'text',
                  placeholder: 'Search volumes…',
                  display: { size: 'sm', leadingIcon: <Search /> },
                }}
                value={labelFilter}
                onChange={v => setLabelFilter(typeof v === 'string' ? v : '')}
              />
            </div>
          }
          notice={volumesQuery.isError && (
            <QueryErrorNotice
              message="Failed to load notebook volumes"
              error={volumesQuery.error}
              onRetry={() => void volumesQuery.refetch()}
            />
          )}
        >
          <DataGrid
            data={filteredVolumes}
            columns={columns}
            rowCursor
            onRowClick={(volume) => open(
              <NotebookVolumeDetailPanel
                volume={volume}
                busy={busy === volume.id}
                onAttach={handleAttach}
                onPurge={handlePurge}
              />,
              { size: 480 },
            )}
            emptyContent={!volumesQuery.isError && (
              <div className="py-12 text-center">
                <p className="text-sm text-muted-foreground">No volumes yet.</p>
                <p className="mt-1 text-xs text-muted-foreground/60">
                  Volumes are created automatically when you launch a notebook server.
                </p>
              </div>
            )}
            tableWidthMode="fill-last"
            rowHeight={44}
            classNames={{ footer: 'pt-3' }}
            pagination={{
              pageSize: PAGE_SIZE,
              pageIndex,
              pageCount: Math.max(1, Math.ceil(total / PAGE_SIZE)),
              onPageChange: setPageIndex,
            }}
            footer={(table) => <DataGridPaginationBar table={table} totalCount={total} />}
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

export default function NotebookVolumesPage() {
  return (
    <SidePanelProvider defaultSize={480} defaultMinSize={380} defaultMaxSize={800}>
      <NotebookVolumesPageInner />
    </SidePanelProvider>
  )
}
