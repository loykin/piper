import { useMemo, useState } from 'react'
import { Search } from 'lucide-react'
import { useNavigate } from '@/lib/router'
import { DataGrid, DataGridPaginationBar } from '@loykin/gridkit'
import { DataBodyTemplate, PageTopBar } from '@loykin/designkit'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { FilterInput } from '@loykin/filter-input'
import { getNotebookVolumeColumns } from '@/features/notebooks/columns'
import { useNotebookVolumesPaged, usePurgeVolume } from '@/features/notebooks/hooks'
import type { NotebookVolume } from '@/features/notebooks/api'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { NotebookVolumeDetailPanel } from '@/features/notebooks/components/NotebookVolumeDetailPanel'
import { useDeleteTarget } from '@/shared/hooks/useDeleteTarget'
import { PageCrumbs } from '@/shared/components/PageCrumbs'
import { useProjectId } from '@/features/projects/context'
import { useTextFilter } from '@/shared/hooks/useTextFilter'
import { ConfirmDialog } from '@/shared/components/ConfirmDialog'

const PAGE_SIZE = 20

function NotebookVolumesPageInner() {
  const { open, close } = useSidePanel()
  const navigate = useNavigate()
  const projectId = useProjectId()
  const [pageIndex, setPageIndex] = useState(0)
  const volumesQuery = useNotebookVolumesPaged(PAGE_SIZE, pageIndex * PAGE_SIZE)
  const total = volumesQuery.data?.total ?? 0
  const { mutateAsync: purgeVolumeAsync, isPending: purging, variables: purgingId } = usePurgeVolume()
  const { target: purgeTarget, open: purgeOpen, error: purgeError, requestDelete: requestPurge, cancel: cancelPurge, confirm: confirmPurge } = useDeleteTarget<NotebookVolume>()
  const [labelFilter, setLabelFilter] = useState('')
  // Filters only the current page — not server-side yet, same accepted
  // trade-off as CredentialsPage's kind filter.
  const filteredVolumes = useTextFilter(volumesQuery.data?.items, labelFilter, v => v.label)

  const busy = purging ? (purgingId ?? null) : null

  const handlePurge = (vol: NotebookVolume) => requestPurge(vol)

  const handleAttach = (volId: string) => navigate(`/projects/${projectId}/notebooks/new?volume=${encodeURIComponent(volId)}`)

  const columns = useMemo(
    () => getNotebookVolumeColumns(busy, handleAttach, handlePurge),
    [busy, handlePurge],
  )

  return (
    <>
    <DataBodyTemplate topBar={<PageTopBar left={<PageCrumbs items={['Development', 'Volumes']} />} />}
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
                id={volume.id}
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

    <ConfirmDialog
      open={purgeOpen}
      onCancel={cancelPurge}
      verb="Purge"
      noun="volume"
      description={`"${purgeTarget?.label}" will permanently delete ${purgeTarget?.work_dir} and all its files. This cannot be undone.`}
      error={purgeError}
      pending={purging}
      onConfirm={() => void confirmPurge(async v => {
        await purgeVolumeAsync(v.id)
        void close()
      })}
    />
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
