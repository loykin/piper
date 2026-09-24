import { Trash2 } from 'lucide-react'
import { PanelTemplate } from '@loykin/designkit'
import { Badge } from '@/components/ui/badge'
import { IconButton } from '@/components/ui/icon-button'
import { PanelCloseButton, PanelPlaceholder } from '@/shared/components/PanelPlaceholder'
import { useUserMemberships, useUsers } from '../hooks'
import type { User } from '../types'

interface Props {
  id: string
  onDelete: (user: User) => void
}

// There is no single-user endpoint; the user directory is small and
// `useUsers` is shared with the member pickers.
export function UserDetailPanel({ id, onDelete }: Props) {
  const query = useUsers()
  const users = query.data
  const { data: memberships = [], isLoading } = useUserMemberships(id)
  const user = users?.find(u => u.id === id)
  if (!user) return <PanelPlaceholder query={query} noun="user" />

  return (
    <PanelTemplate
      eyebrow="System Account"
      title={user.username}
      status={
        <Badge variant={user.disabled ? 'secondary' : 'outline'}>
          {user.disabled ? 'Disabled' : 'Active'}
        </Badge>
      }
      actions={
        <div className="flex items-center gap-1">
          <IconButton
            icon={<Trash2 />}
            label={`Delete ${user.username}`}
            className="text-destructive hover:bg-destructive/10"
            onClick={() => onDelete(user)}
          />
          <PanelCloseButton />
        </div>
      }
    >
      <PanelTemplate.Section title="Account">
        <dl className="space-y-2">
          <PanelTemplate.Row label="Username">{user.username}</PanelTemplate.Row>
          <PanelTemplate.Row label="Access">{user.system_admin ? 'System administrator' : 'Standard user'}</PanelTemplate.Row>
        </dl>
      </PanelTemplate.Section>
      <PanelTemplate.Section title="Project Roles">
        {isLoading ? (
          <p className="text-xs text-muted-foreground">Loading memberships…</p>
        ) : memberships.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            No project memberships. System administrators can access every project without a membership.
          </p>
        ) : (
          <div className="space-y-2">
            {memberships.map(membership => (
              <div key={membership.project_id} className="flex items-center justify-between">
                <span className="font-mono text-xs">{membership.project_id}</span>
                <Badge variant="outline">{membership.role}</Badge>
              </div>
            ))}
          </div>
        )}
      </PanelTemplate.Section>
    </PanelTemplate>
  )
}
