import { useState } from 'react'
import { Search } from 'lucide-react'
import { DataBodyTemplate, PageTopBar } from '@loykin/designkit'
import { DataGrid, DataGridPaginationBar } from '@loykin/gridkit'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { FilterInput } from '@loykin/filter-input'
import { userColumns } from '@/features/access/columns'
import { UserDetailPanel } from '@/features/access/components/UserDetailPanel'
import { useDeleteUser, useUsersPaged } from '@/features/access/hooks'
import type { User } from '@/features/access/types'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { useDeleteTarget } from '@/shared/hooks/useDeleteTarget'
import { PageCrumbs } from '@/shared/components/PageCrumbs'
import { useTextFilter } from '@/shared/hooks/useTextFilter'
import { ConfirmDialog } from '@/shared/components/ConfirmDialog'
import { CreateButton } from '@/shared/components/CreateButton'

const PAGE_SIZE = 20

function UsersPageInner() {
  const { open, close } = useSidePanel()
  const [pageIndex, setPageIndex] = useState(0)
  const usersQuery = useUsersPaged(PAGE_SIZE, pageIndex * PAGE_SIZE)
  const total = usersQuery.data?.total ?? 0
  const deleteUser = useDeleteUser()
  const { target: deleteTarget, open: deleteOpen, error: deleteError, requestDelete, cancel: cancelDelete, confirm: confirmDeleteTarget } = useDeleteTarget<User>()
  const [nameFilter, setNameFilter] = useState('')
  // Filters only the current page — not server-side yet, same accepted
  // trade-off as CredentialsPage's kind filter.
  const filteredUsers = useTextFilter(usersQuery.data?.items, nameFilter, u => u.username)

  function confirmDelete() {
    return confirmDeleteTarget(async t => {
      await deleteUser.mutateAsync(t.id)
      void close()
    })
  }

  return (
    <>
      <DataBodyTemplate topBar={<PageTopBar left={<PageCrumbs items={['System', 'Users']} />} />}
        title="Users"
        description="System accounts and administrator access."
      >
        <DataBodyTemplate.Body>
          <DataBodyTemplate.Resource
            toolbarLeft={
              <div className="w-48">
                <FilterInput
                  config={{
                    key: 'userSearch',
                    type: 'text',
                    placeholder: 'Search users…',
                    display: { size: 'sm', leadingIcon: <Search /> },
                  }}
                  value={nameFilter}
                  onChange={v => setNameFilter(typeof v === 'string' ? v : '')}
                />
              </div>
            }
            toolbarRight={<CreateButton noun="User" to="/users/new" />}
            notice={usersQuery.isError && (
                  <QueryErrorNotice
                    message="Failed to load users"
                    error={usersQuery.error}
                    onRetry={() => void usersQuery.refetch()}
                  />
            )}
          >
            <DataGrid
              data={filteredUsers}
              columns={userColumns}
              isLoading={usersQuery.isLoading}
              emptyMessage={usersQuery.isError ? undefined : 'No users yet.'}
              tableWidthMode="fill-last"
              rowHeight={44}
              rowCursor
              onRowClick={(user) => open(
                <UserDetailPanel id={user.id} onDelete={requestDelete} />,
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
        open={deleteOpen}
        onCancel={cancelDelete}
        verb="Delete"
        noun="user"
        description={`${deleteTarget?.username} will lose access immediately. This action cannot be undone.`}
        error={deleteError}
        pending={deleteUser.isPending}
        onConfirm={() => void confirmDelete()}
      />
    </>
  )
}

export default function UsersPage() {
  return (
    <SidePanelProvider defaultSize={520} defaultMinSize={380} defaultMaxSize={900}>
      <UsersPageInner />
    </SidePanelProvider>
  )
}
