import { useMemo, useState } from 'react'
import { Search } from 'lucide-react'
import { useNavigate } from '@/lib/router'
import { useProjectId } from '@/features/projects/context'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { DataGrid, DataGridPaginationBar } from '@loykin/gridkit'
import { DataBodyTemplate, PageTopBar } from '@loykin/designkit'
import { FilterInput } from '@loykin/filter-input'
import { Button } from '@/components/ui/button'
import { getNotebookColumns } from '@/features/notebooks/columns'
import { NotebookDetailPanel } from '@/features/notebooks/components/NotebookDetailPanel'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { useDeleteTarget } from '@/shared/hooks/useDeleteTarget'
import {
  useNotebooksPaged, useNotebookVolumes,
  useStopNotebook, useStartNotebook, useDeleteNotebook,
} from '@/features/notebooks/hooks'
import { PageCrumbs } from '@/shared/components/PageCrumbs'
import { useTextFilter } from '@/shared/hooks/useTextFilter'
import { ConfirmDialog } from '@/shared/components/ConfirmDialog'

const PAGE_SIZE = 20

function NotebooksPageInner() {
  const navigate = useNavigate()
  const projectId = useProjectId()
  const { open } = useSidePanel()
  const [pageIndex, setPageIndex] = useState(0)
  const notebooksQuery = useNotebooksPaged(PAGE_SIZE, pageIndex * PAGE_SIZE)
  const total = notebooksQuery.data?.total ?? 0
  const [nameFilter, setNameFilter] = useState('')
  // Filters the current page only — same accepted trade-off as the other
  // paginated list pages until the endpoint supports server-side search.
  const filteredNotebooks = useTextFilter(notebooksQuery.data?.items, nameFilter, n => n.name)
  const { data: allVolumes = [] } = useNotebookVolumes()
  const releasedVolumes = useMemo(() => allVolumes.filter(v => v.status === 'released'), [allVolumes])

  const { mutate: stop, isPending: stopping, variables: stoppingName } = useStopNotebook()
  const { mutate: start, isPending: starting, variables: startingName } = useStartNotebook()
  const { mutateAsync: deleteAsync, isPending: deleting, variables: deletingName } = useDeleteNotebook()
  const { target: deleteTarget, open: deleteOpen, requestDelete, cancel: cancelDelete, confirm: confirmDeleteTarget } = useDeleteTarget<string>()

  const busy = stopping ? (stoppingName ?? null)
    : starting ? (startingName ?? null)
    : deleting ? (deletingName ?? null)
    : null

  const handleStop   = (name: string) => stop(name)
  const handleStart  = (name: string) => start(name)
  const handleDelete = (name: string) => requestDelete(name)

  const columns = useMemo(
    () => getNotebookColumns(busy, handleStop, handleStart, handleDelete, projectId),
    [busy, handleDelete],
  )

  return (
    <>
    <DataBodyTemplate topBar={<PageTopBar left={<PageCrumbs items={['Development', 'Notebooks']} />} />}
      title="Notebooks"
      description="Jupyter notebook servers. Open launches a server in a new tab."
    >
      <DataBodyTemplate.Body>
        <DataBodyTemplate.Resource
          toolbarLeft={
            <div className="w-48">
              <FilterInput
                config={{
                  key: 'notebookSearch',
                  type: 'text',
                  placeholder: 'Search notebooks…',
                  display: { size: 'sm', leadingIcon: <Search /> },
                }}
                value={nameFilter}
                onChange={v => setNameFilter(typeof v === 'string' ? v : '')}
              />
            </div>
          }
          toolbarRight={
            <Button size="sm" onClick={() => navigate(`/projects/${projectId}/notebooks/new`)}>Launch</Button>
          }
          notice={notebooksQuery.isError && (
            <QueryErrorNotice
              message="Failed to load notebooks"
              error={notebooksQuery.error}
              onRetry={() => void notebooksQuery.refetch()}
            />
          )}
        >
          <DataGrid
            data={filteredNotebooks}
            columns={columns}
            emptyContent={!notebooksQuery.isError && (
              <div className="py-12 text-center">
                <p className="text-sm text-muted-foreground">No notebook servers running.</p>
                {releasedVolumes.length > 0 && (
                  <p className="mt-1 text-xs text-muted-foreground/60">
                    {releasedVolumes.length} released volume{releasedVolumes.length > 1 ? 's' : ''} available — click Launch to attach one.
                  </p>
                )}
              </div>
            )}
            tableWidthMode="fill-last"
            rowHeight={44}
            rowCursor
            onRowClick={(row) => open(<NotebookDetailPanel name={row.name} projectId={projectId} />, { size: 520 })}
            classNames={{ footer: 'pt-3' }}
            pagination={{
              pageSize: PAGE_SIZE,
              pageIndex,
              pageCount: Math.max(1, Math.ceil(total / PAGE_SIZE)),
              onPageChange: setPageIndex,
            }}
            footer={(table) => (
              <div className="flex flex-col gap-2">
                {releasedVolumes.length > 0 && (
                  <div className="text-xs text-muted-foreground">
                    <Button
                      type="button"
                      variant="link"
                      size="sm"
                      className="h-auto p-0 text-xs"
                      onClick={() => navigate(`/projects/${projectId}/notebooks/new?volume=${encodeURIComponent(releasedVolumes[0].id)}`)}
                    >
                      {releasedVolumes.length} released volume{releasedVolumes.length > 1 ? 's' : ''} — Attach
                    </Button>
                  </div>
                )}
                <DataGridPaginationBar table={table} totalCount={total} />
              </div>
            )}
          />
        </DataBodyTemplate.Resource>
      </DataBodyTemplate.Body>
    </DataBodyTemplate>

    <ConfirmDialog
      open={deleteOpen}
      onCancel={cancelDelete}
      title="Delete this notebook?"
      description={<>"{deleteTarget}" will be deleted. The volume and work directory are preserved — you can recover them from the Volumes page.</>}
      confirmLabel={deleting ? 'Deleting…' : 'Delete notebook'}
      pending={deleting}
      onConfirm={() => void confirmDeleteTarget(name => deleteAsync(name))}
    />
    </>
  )
}

export default function NotebooksPage() {
  return (
    <SidePanelProvider defaultSize={520} defaultMinSize={380} defaultMaxSize={900}>
      <NotebooksPageInner />
    </SidePanelProvider>
  )
}
