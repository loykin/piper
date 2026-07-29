import { useMemo, useState } from 'react'
import { DataBodyTemplate } from '@loykin/designkit'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
  AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { DataGrid, type DataGridColumnDef } from '@loykin/gridkit'
import { Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { IconButton } from '@/components/ui/icon-button'
import { useCreateUser, useDeleteUser, useUsers } from '@/features/access/hooks'
import type { User } from '@/features/access/types'

export default function UsersPage() {
  const { data: users = [] } = useUsers()
  const { mutateAsync: createUser, isPending: creating } = useCreateUser()
  const { mutate: deleteUser } = useDeleteUser()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [systemAdmin, setSystemAdmin] = useState(false)
  const [error, setError] = useState('')
  const [deleteTarget, setDeleteTarget] = useState<User | null>(null)

  const columns = useMemo<DataGridColumnDef<User>[]>(() => [
    { accessorKey: 'email', header: 'Email' },
    { accessorKey: 'id', header: 'User ID' },
    { accessorKey: 'display_name', header: 'Display name' },
    {
      accessorKey: 'system_admin',
      header: 'System admin',
      cell: ({ row }) => row.original.system_admin ? 'Yes' : 'No',
    },
    {
      accessorKey: 'disabled',
      header: 'Status',
      cell: ({ row }) => row.original.disabled ? 'Disabled' : 'Active',
    },
    {
      id: 'actions',
      header: '',
      cell: ({ row }) => (
        <IconButton
          icon={<Trash2 />}
          label={`Delete ${row.original.email}`}
          onClick={() => setDeleteTarget(row.original)}
        />
      ),
    },
  ], [])

  async function submit() {
    setError('')
    try {
      await createUser({ email, password, system_admin: systemAdmin })
      setEmail('')
      setPassword('')
      setSystemAdmin(false)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    }
  }

  return (
    <DataBodyTemplate title="Users" description="Manage system accounts and administrator access.">
      <DataBodyTemplate.Body>
        <DataBodyTemplate.Group layout="stacked" variant="bordered" title="Create user">
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <DataBodyTemplate.Field label="Email">
              <Input type="email" value={email} onChange={event => setEmail(event.target.value)} />
            </DataBodyTemplate.Field>
            <DataBodyTemplate.Field label="Temporary password">
              <Input type="password" value={password} onChange={event => setPassword(event.target.value)} />
            </DataBodyTemplate.Field>
          </div>
          <DataBodyTemplate.Field label="System administrator">
            <Switch checked={systemAdmin} onCheckedChange={setSystemAdmin} />
          </DataBodyTemplate.Field>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <div>
            <Button size="sm" disabled={creating || !email || !password} onClick={() => void submit()}>
              {creating ? 'Creating…' : 'Create user'}
            </Button>
          </div>
        </DataBodyTemplate.Group>
        <DataGrid data={users} columns={columns} emptyMessage="No users found." tableWidthMode="fill-last" />
      </DataBodyTemplate.Body>
      <AlertDialog open={deleteTarget != null} onOpenChange={open => { if (!open) setDeleteTarget(null) }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this user?</AlertDialogTitle>
            <AlertDialogDescription>{deleteTarget?.email} will lose access immediately.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                if (deleteTarget) deleteUser(deleteTarget.id)
                setDeleteTarget(null)
              }}
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </DataBodyTemplate>
  )
}
