import { useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { useProjectId } from '@/lib/projectContext'
import { DataGrid, DataGridPaginationCompact } from '@loykin/gridkit'
import { DataPage } from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { getNotebookColumns } from '@/features/notebooks/columns'
import {
  useNotebooks, useNotebookVolumes,
  useStopNotebook, useStartNotebook, useDeleteNotebook,
} from '@/features/notebooks/hooks'

export default function NotebooksPage() {
  const navigate = useNavigate()
  const projectId = useProjectId()
  const { data: notebooks = [], isLoading } = useNotebooks()
  const { data: allVolumes = [] } = useNotebookVolumes()
  const releasedVolumes = useMemo(() => allVolumes.filter(v => v.status === 'released'), [allVolumes])

  const { mutate: stop, isPending: stopping, variables: stoppingName } = useStopNotebook()
  const { mutate: start, isPending: starting, variables: startingName } = useStartNotebook()
  const { mutate: del, isPending: deleting, variables: deletingName } = useDeleteNotebook()

  const busy = stopping ? (stoppingName ?? null)
    : starting ? (startingName ?? null)
    : deleting ? (deletingName ?? null)
    : null

  const handleStop   = (name: string) => stop(name)
  const handleStart  = (name: string) => start(name)
  const handleDelete = (name: string) => {
    if (!confirm(`Delete notebook "${name}"?\nThe volume and work directory are preserved. You can recover them from the Volumes page.`)) return
    del(name)
  }

  const columns = useMemo(
    () => getNotebookColumns(busy, handleStop, handleStart, handleDelete, projectId),
    [busy],
  )

  return (
    <DataPage>
      <DataPage.Header>
        <DataPage.TitleBlock
          title="Notebooks"
          description="Jupyter notebook servers. Click Open to launch in a new tab."
        />
        <DataPage.Actions>
          <Button size="sm" onClick={() => navigate(`/projects/${projectId}/notebooks/create`)}>Launch</Button>
        </DataPage.Actions>
      </DataPage.Header>

      <DataPage.Content>
        {isLoading ? (
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
                      <Button
                        type="button"
                        variant="link"
                        size="sm"
                        className="h-auto p-0 text-xs"
                        onClick={() => navigate(`/projects/${projectId}/notebooks/create?volume=${releasedVolumes[0].id}`)}
                      >
                        {releasedVolumes.length} released volume{releasedVolumes.length > 1 ? 's' : ''} — Attach
                      </Button>
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
