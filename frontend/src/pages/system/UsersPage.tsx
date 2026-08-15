import { useState } from 'react'
import { Plus } from 'lucide-react'
import { DataBodyTemplate } from '@loykin/designkit'
import { DataGrid } from '@loykin/gridkit'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
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
import { useDeleteUser, useUsers } from '@/features/access/hooks'
import type { User } from '@/features/access/types'
import { useNavigate } from '@/lib/router'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'

function UsersPageInner() {
  const navigate = useNavigate()
  const { open } = useSidePanel()
  const usersQuery = useUsers()
  const users = usersQuery.data ?? []
  const deleteUser = useDeleteUser()
  const [deleteTarget, setDeleteTarget] = useState<User | null>(null)
  const [actionError, setActionError] = useState('')

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
        description="System accounts and administrator access. Click a row to view account details."
        actions={
          <Button size="sm" onClick={() => void navigate('/users/new')}>
            <Plus />
            New User
          </Button>
        }
      >
        <DataBodyTemplate.Body>
          {usersQuery.isError && (
            <QueryErrorNotice
              message="Failed to load users"
              error={usersQuery.error}
              onRetry={() => void usersQuery.refetch()}
            />
          )}
          {actionError && <p className="mb-3 text-sm text-destructive">{actionError}</p>}
          <DataGrid
            data={users}
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
          />
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
