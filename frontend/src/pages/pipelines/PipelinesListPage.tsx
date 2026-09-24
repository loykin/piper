import { useState } from 'react'
import type { Row } from '@tanstack/react-table'
import { useNavigate, useSearchParams } from '@/lib/router'
import { useProjectId } from '@/features/projects/context'
import { Search } from 'lucide-react'
import { DataBodyTemplate, PageTopBar } from '@loykin/designkit'
import { FilterInput } from '@loykin/filter-input'
import { DataGrid, DataGridPaginationBar } from '@loykin/gridkit'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { usePipelinesPaged, useDeletePipeline, useRunPipeline } from '@/features/pipelines/hooks'
import { usePipelineColumns } from '@/features/pipelines/columns'
import { PipelineDetailPanel } from '@/features/pipelines/components/PipelineDetailPanel'
import type { PipelineTemplate } from '@/features/pipelines/types'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { useDeleteTarget } from '@/shared/hooks/useDeleteTarget'
import { errorMessage } from '@/lib/format'
import { PageCrumbs } from '@/shared/components/PageCrumbs'
import { useTextFilter } from '@/shared/hooks/useTextFilter'
import { ConfirmDialog } from '@/shared/components/ConfirmDialog'
import { CreateButton } from '@/shared/components/CreateButton'

const PAGE_SIZE = 20

function GroupHeader({ row }: { row: Row<PipelineTemplate> }) {
  const first = row.subRows[0]?.original
  const name = first?.name ?? '—'
  const description = first?.description
  const count = row.subRows.length
  return (
    <span className="flex items-baseline gap-2">
      <span className="font-medium text-foreground">{name}</span>
      {description && <span className="text-xs text-muted-foreground">{description}</span>}
      <span className="text-xs font-normal text-muted-foreground">{count} version{count !== 1 ? 's' : ''}</span>
    </span>
  )
}

function PipelinesListPageInner() {
  const navigate = useNavigate()
  const projectId = useProjectId()
  const { open, close } = useSidePanel()
  const [searchParams] = useSearchParams()
  const filterName = searchParams.get('name') ?? ''
  const [pageIndex, setPageIndex] = useState(0)

  const {
    data: templateData,
    error: loadError,
    isError: loadFailed,
    isLoading: templatesLoading,
    refetch: refetchTemplates,
  } = usePipelinesPaged(filterName || undefined, PAGE_SIZE, pageIndex * PAGE_SIZE)
  const total = templateData?.total ?? 0
  const initialLoadFailed = loadFailed && templateData === undefined
  const [searchFilter, setSearchFilter] = useState('')
  // Filters only the current page — not server-side yet, same accepted
  // trade-off as CredentialsPage's kind filter. Separate from `filterName`
  // (the ?name= URL param), which is an exact-match server-side filter used
  // by the "jump back to this template after create" flow, not a search box.
  const filteredTemplates = useTextFilter(templateData?.items, searchFilter, t => t.name)
  const { mutateAsync: deletePipeline, isPending: deleting } = useDeletePipeline()
  const { mutateAsync: runPipeline } = useRunPipeline()

  const [actionError, setActionError] = useState('')
  const { target: deleteTarget, open: deleteOpen, error: deleteError, requestDelete, cancel: cancelDelete, confirm: confirmDeleteTarget } = useDeleteTarget<PipelineTemplate>()

  async function handleRun(t: PipelineTemplate) {
    setActionError('')
    try {
      const result = await runPipeline({ id: t.id })
      navigate(`/projects/${projectId}/runs/${result.id}`)
    } catch (err) {
      setActionError(errorMessage(err))
    }
  }

  function confirmDelete() {
    return confirmDeleteTarget(async t => {
      await deletePipeline(t.id)
      void close()
    })
  }

  function openDeploy(t: PipelineTemplate) {
    navigate(`/projects/${projectId}/pipelines/${t.id}/deploy`)
  }

  function openNewVersionFrom(t: PipelineTemplate) {
    const params = new URLSearchParams({
      from_version: t.id,
      name: t.name,
      source: t.volume_id ? 'notebook-volume' : 'local',
    })
    if (t.volume_id) params.set('volume', t.volume_id)
    else params.set('root', '.')
    navigate(`/projects/${projectId}/pipelines/editor?${params.toString()}`)
  }

  function openDetail(t: PipelineTemplate) {
    open(
      <PipelineDetailPanel
        id={t.id}
        onRun={(x) => void handleRun(x)}
        onDeploy={openDeploy}
        onNewVersion={openNewVersionFrom}
        onDelete={requestDelete}
      />,
      { size: 520 },
    )
  }

  const columns = usePipelineColumns({
    onRun: (t) => void handleRun(t),
    onDeploy: openDeploy,
    onNewVersion: openNewVersionFrom,
    onDelete: requestDelete,
  })

  return (
    <>
      <DataBodyTemplate topBar={<PageTopBar left={<PageCrumbs items={['Pipelines', 'Templates']} />} />}
        title="Pipeline Templates"
        description="Each submit creates a new versioned snapshot. Deploy to schedule or run on demand."
      >
        <DataBodyTemplate.Body>
          <DataBodyTemplate.Resource
            toolbarLeft={
              <div className="w-48">
                <FilterInput
                  config={{
                    key: 'templateSearch',
                    type: 'text',
                    placeholder: 'Search templates…',
                    display: { size: 'sm', leadingIcon: <Search /> },
                  }}
                  value={searchFilter}
                  onChange={v => setSearchFilter(typeof v === 'string' ? v : '')}
                />
              </div>
            }
            toolbarRight={<CreateButton noun="Template" to={`/projects/${projectId}/pipelines/editor`} />}
            notice={(initialLoadFailed || actionError) && (
              <>
                {initialLoadFailed && (
                  <QueryErrorNotice
                    message="Failed to load pipeline templates"
                    error={loadError}
                    onRetry={() => void refetchTemplates()}
                  />
                )}
                {actionError && <p className="text-sm text-destructive">{actionError}</p>}
              </>
            )}
          >
            <DataGrid
              data={filteredTemplates}
              columns={columns}
              isLoading={templatesLoading}
              enableGrouping
              grouping={['name']}
              visibilityState={{ name: false }}
              renderGroupRow={(row) => <GroupHeader row={row} />}
              emptyMessage={loadFailed ? undefined : 'No pipeline templates yet.'}
              tableWidthMode="fill-last"
              rowHeight={44}
              rowCursor
              onRowClick={(row) => openDetail(row)}
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
        open={deleteOpen}
        onCancel={cancelDelete}
        verb="Delete"
        noun="pipeline template"
        description={<>"{deleteTarget?.name}" v{deleteTarget?.version} ({deleteTarget?.id.slice(0, 8)}…) and its snapshot will be permanently deleted.</>}
        error={deleteError}
        pending={deleting}
        onConfirm={() => void confirmDelete()}
      />
    </>
  )
}

export default function PipelinesListPage() {
  return (
    <SidePanelProvider defaultSize={520} defaultMinSize={380} defaultMaxSize={900}>
      <PipelinesListPageInner />
    </SidePanelProvider>
  )
}
