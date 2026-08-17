import { useMemo, useState } from 'react'
import { Plus, Search } from 'lucide-react'
import { DataBodyTemplate } from '@loykin/designkit'
import { DataGrid, DataGridPaginationBar } from '@loykin/gridkit'
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
import { Button } from '@/components/ui/button'
import { userColumns } from '@/features/access/columns'
import { UserDetailPanel } from '@/features/access/components/UserDetailPanel'
import { useDeleteUser, useUsersPaged } from '@/features/access/hooks'
import type { User } from '@/features/access/types'
import { useNavigate } from '@/lib/router'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'

const PAGE_SIZE = 20

function UsersPageInner() {
  const navigate = useNavigate()
  const { open } = useSidePanel()
  const [pageIndex, setPageIndex] = useState(0)
  const usersQuery = useUsersPaged(PAGE_SIZE, pageIndex * PAGE_SIZE)
  const total = usersQuery.data?.total ?? 0
  const deleteUser = useDeleteUser()
  const [deleteTarget, setDeleteTarget] = useState<User | null>(null)
  const [actionError, setActionError] = useState('')
  const [nameFilter, setNameFilter] = useState('')
  // Filters only the current page — not server-side yet, same accepted
  // trade-off as CredentialsPage's kind filter.
  const filteredUsers = useMemo(() => {
    const list = usersQuery.data?.users ?? []
    if (!nameFilter.trim()) return list
    const q = nameFilter.trim().toLowerCase()
    return list.filter(u => u.username.toLowerCase().includes(q))
  }, [usersQuery.data, nameFilter])

  async function confirmDelete() {
    if (!deleteTarget) return
    setActionError('')
    try {
      await deleteUser.mutateAsync(deleteTarget.id)
      setDeleteTarget(null)
    } catch (cause) {
      setActionError(cause instanceof Error ? cause.message : String(cause))
    }
  }

  return (
    <>
      <DataBodyTemplate
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
            toolbarRight={
              <Button size="sm" onClick={() => void navigate('/users/new')}>
                <Plus />
                New User
              </Button>
            }
            notice={(usersQuery.isError || actionError) && (
              <>
                {usersQuery.isError && (
                  <QueryErrorNotice
                    message="Failed to load users"
                    error={usersQuery.error}
                    onRetry={() => void usersQuery.refetch()}
                  />
                )}
                {actionError && <p className="text-sm text-destructive">{actionError}</p>}
              </>
            )}
          >
            <DataGrid
              data={filteredUsers}
              columns={userColumns}
              isLoading={usersQuery.isLoading}
              emptyMessage="No users found."
              tableWidthMode="fill-last"
              rowHeight={44}
              rowCursor
              onRowClick={(user) => open(
                <UserDetailPanel user={user} onDelete={setDeleteTarget} />,
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

      <AlertDialog open={deleteTarget != null} onOpenChange={open => { if (!open) setDeleteTarget(null) }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this user?</AlertDialogTitle>
            <AlertDialogDescription>
              {deleteTarget?.username} will lose access immediately. This action cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={deleteUser.isPending}
              onClick={() => void confirmDelete()}
            >
              {deleteUser.isPending ? 'Deleting…' : 'Delete user'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
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
