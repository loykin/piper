import { Trash2, X } from 'lucide-react'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { useUserMemberships } from '../hooks'
import type { User } from '../types'

interface Props {
  user: User
  onDelete: (user: User) => void
}

export function UserDetailPanel({ user, onDelete }: Props) {
  const { close } = useSidePanel()
  const { data: memberships = [], isLoading } = useUserMemberships(user.id)

  return (
    <PanelTemplate
      eyebrow="System account"
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
            onClick={() => {
              onDelete(user)
              void close()
            }}
          />
          <Button variant="ghost" size="icon-sm" onClick={() => void close()}>
            <X />
            <span className="sr-only">Close</span>
          </Button>
        </div>
      }
    >
      <PanelTemplate.Section title="Account">
        <dl className="space-y-2">
          <PanelTemplate.Row label="Username">{user.username}</PanelTemplate.Row>
          <PanelTemplate.Row label="Access">{user.system_admin ? 'System administrator' : 'Standard user'}</PanelTemplate.Row>
        </dl>
      </PanelTemplate.Section>
      <PanelTemplate.Section title="Project roles">
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
