import { useMemo, useState } from 'react'
import {
  DataBodyTemplate, Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from '@loykin/designkit'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
  AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { DataGrid, type DataGridColumnDef } from '@loykin/gridkit'
import { Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { IconButton } from '@/components/ui/icon-button'
import { useAuth } from '@/features/auth/context'
import {
  useAddMember, useMembers, useRemoveMember, useUpdateMember, useUsers,
} from '@/features/access/hooks'
import type { ProjectMember, ProjectRole } from '@/features/access/types'

export default function MembersPage() {
  const { user } = useAuth()
  const { data: members = [] } = useMembers()
  const { data: users = [] } = useUsers(user?.system_admin === true)
  const { mutateAsync: addMember, isPending: adding } = useAddMember()
  const { mutate: updateMember } = useUpdateMember()
  const { mutate: removeMember } = useRemoveMember()
  const [userId, setUserId] = useState('')
  const [role, setRole] = useState<ProjectRole>('member')
  const [removeTarget, setRemoveTarget] = useState<ProjectMember | null>(null)

  const columns = useMemo<DataGridColumnDef<ProjectMember>[]>(() => [
    {
      accessorKey: 'user_id',
      header: 'User',
      cell: ({ row }) => users.find(user => user.id === row.original.user_id)?.email ?? row.original.user_id,
    },
    {
      accessorKey: 'role',
      header: 'Role',
      cell: ({ row }) => (
        <Select
          value={row.original.role}
          onValueChange={value => updateMember({ userId: row.original.user_id, role: value as ProjectRole })}
        >
          <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="viewer">Viewer</SelectItem>
            <SelectItem value="member">Member</SelectItem>
            <SelectItem value="admin">Admin</SelectItem>
          </SelectContent>
        </Select>
      ),
    },
    {
      id: 'actions',
      header: '',
      cell: ({ row }) => (
        <IconButton icon={<Trash2 />} label="Remove member" onClick={() => setRemoveTarget(row.original)} />
      ),
    },
  ], [updateMember, users])

  return (
    <DataBodyTemplate title="Project Members" description="Manage project roles and access.">
      <DataBodyTemplate.Body>
        <DataBodyTemplate.Group layout="inline" variant="bordered" title="Add member">
          <DataBodyTemplate.Field label="User">
            {user?.system_admin ? (
              <Select value={userId} onValueChange={value => setUserId(value ?? '')}>
                <SelectTrigger size="sm"><SelectValue placeholder="Select a user" /></SelectTrigger>
                <SelectContent>
                  {users.map(candidate => <SelectItem key={candidate.id} value={candidate.id}>{candidate.email}</SelectItem>)}
                </SelectContent>
              </Select>
            ) : (
              <Input value={userId} onChange={event => setUserId(event.target.value)} placeholder="User ID" />
            )}
          </DataBodyTemplate.Field>
          <DataBodyTemplate.Field label="Role">
            <Select value={role} onValueChange={value => setRole(value as ProjectRole)}>
              <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="viewer">Viewer</SelectItem>
                <SelectItem value="member">Member</SelectItem>
                <SelectItem value="admin">Admin</SelectItem>
              </SelectContent>
            </Select>
          </DataBodyTemplate.Field>
          <Button size="sm" disabled={adding || !userId} onClick={() => void addMember({ userId, role }).then(() => setUserId(''))}>
            {adding ? 'Adding…' : 'Add member'}
          </Button>
        </DataBodyTemplate.Group>
        <DataGrid data={members} columns={columns} emptyMessage="No project members." tableWidthMode="fill-last" />
      </DataBodyTemplate.Body>
      <AlertDialog open={removeTarget != null} onOpenChange={open => { if (!open) setRemoveTarget(null) }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove this project member?</AlertDialogTitle>
            <AlertDialogDescription>The user will lose access to this project.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                if (removeTarget) removeMember(removeTarget.user_id)
                setRemoveTarget(null)
              }}
            >
              Remove
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </DataBodyTemplate>
  )
}
