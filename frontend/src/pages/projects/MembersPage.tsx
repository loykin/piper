import { useState } from 'react'
import { Plus, Search } from 'lucide-react'
import { DataBodyTemplate, PageTopBar } from '@loykin/designkit'
import { DataGrid, DataGridPaginationBar } from '@loykin/gridkit'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { FilterInput } from '@loykin/filter-input'
import { Button } from '@/components/ui/button'
import { memberColumns } from '@/features/access/memberColumns'
import { MemberDetailPanel } from '@/features/access/components/MemberDetailPanel'
import { useMembersPaged, useRemoveMember } from '@/features/access/hooks'
import type { ProjectMember } from '@/features/access/types'
import { useProjectId } from '@/features/projects/context'
import { useNavigate } from '@/lib/router'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { useDeleteTarget } from '@/shared/hooks/useDeleteTarget'
import { PageCrumbs } from '@/shared/components/PageCrumbs'
import { useTextFilter } from '@/shared/hooks/useTextFilter'
import { ConfirmDialog } from '@/shared/components/ConfirmDialog'

const PAGE_SIZE = 20

function MembersPageInner() {
  const projectId = useProjectId()
  const navigate = useNavigate()
  const { open } = useSidePanel()
  const [pageIndex, setPageIndex] = useState(0)
  const membersQuery = useMembersPaged(PAGE_SIZE, pageIndex * PAGE_SIZE)
  const total = membersQuery.data?.total ?? 0
  const removeMember = useRemoveMember()
  const { target: removeTarget, open: removeOpen, error: actionError, requestDelete: requestRemove, cancel: cancelRemove, confirm: confirmRemoveTarget } = useDeleteTarget<ProjectMember>()
  const [nameFilter, setNameFilter] = useState('')
  // Filters only the current page — not server-side yet, same accepted
  // trade-off as CredentialsPage's kind filter.
  const filteredMembers = useTextFilter(membersQuery.data?.items, nameFilter, m => m.username)

  function confirmRemove() {
    return confirmRemoveTarget(t => removeMember.mutateAsync(t.user_id))
  }

  return (
    <>
      <DataBodyTemplate topBar={<PageTopBar left={<PageCrumbs items={['Infrastructure', 'Members']} />} />}
        title="Project Members"
        description="Project-specific access for Piper user accounts."
      >
        <DataBodyTemplate.Body>
          <DataBodyTemplate.Resource
            toolbarLeft={
              <div className="w-48">
                <FilterInput
                  config={{
                    key: 'memberSearch',
                    type: 'text',
                    placeholder: 'Search members…',
                    display: { size: 'sm', leadingIcon: <Search /> },
                  }}
                  value={nameFilter}
                  onChange={v => setNameFilter(typeof v === 'string' ? v : '')}
                />
              </div>
            }
            toolbarRight={
              <Button size="sm" onClick={() => void navigate(`/projects/${projectId}/members/new`)}>
                <Plus />
                New Member
              </Button>
            }
            notice={(membersQuery.isError || actionError) && (
              <>
                {membersQuery.isError && (
                  <QueryErrorNotice
                    message="Failed to load project members"
                    error={membersQuery.error}
                    onRetry={() => void membersQuery.refetch()}
                  />
                )}
                {actionError && <p className="text-sm text-destructive">{actionError}</p>}
              </>
            )}
          >
            <DataGrid
              data={filteredMembers}
              columns={memberColumns}
              isLoading={membersQuery.isLoading}
              emptyMessage={membersQuery.isError ? undefined : 'No project members.'}
              tableWidthMode="fill-last"
              rowHeight={44}
              rowCursor
              onRowClick={member => open(
                <MemberDetailPanel member={member} onRemove={requestRemove} />,
                { size: 520 },
              )}
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
        open={removeOpen}
        onCancel={cancelRemove}
        title="Remove this project member?"
        description={<>{removeTarget?.username || 'This user'} will lose access to this project.</>}
        confirmLabel={removeMember.isPending ? 'Removing…' : 'Remove member'}
        pending={removeMember.isPending}
        onConfirm={() => void confirmRemove()}
      />
    </>
  )
}

export default function MembersPage() {
  return (
    <SidePanelProvider defaultSize={520} defaultMinSize={380} defaultMaxSize={900}>
      <MembersPageInner />
    </SidePanelProvider>
  )
}
