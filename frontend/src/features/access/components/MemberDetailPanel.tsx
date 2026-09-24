import { Trash2 } from 'lucide-react'
import { PanelGuard } from '@loykin/side-panel'
import {
  PanelTemplate,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@loykin/designkit'
import { IconButton } from '@/components/ui/icon-button'
import { errorMessage } from '@/lib/format'
import { PanelCloseButton, PanelPlaceholder } from '@/shared/components/PanelPlaceholder'
import { useMembers, useUpdateMember } from '../hooks'
import { PROJECT_ROLE_ITEMS, type ProjectMember, type ProjectRole } from '../types'

interface Props {
  userId: string
  onRemove: (member: ProjectMember) => void
}

// There is no single-member endpoint; the project's full member list is
// small and already cached by other pages.
export function MemberDetailPanel({ userId, onRemove }: Props) {
  const query = useMembers()
  const members = query.data
  const updateMember = useUpdateMember()
  const member = members?.find(m => m.user_id === userId)
  if (!member) return <PanelPlaceholder query={query} noun="member" />

  return (
    <PanelTemplate
      eyebrow="Project Membership"
      title={member.username || 'Unknown user'}
      actions={
        <div className="flex items-center gap-1">
          <IconButton
            icon={<Trash2 />}
            label={`Remove ${member.username || 'member'}`}
            className="text-destructive hover:bg-destructive/10"
            onClick={() => onRemove(member)}
          />
          <PanelCloseButton />
        </div>
      }
    >
      <PanelTemplate.Section title="Access">
        <div className="space-y-1.5">
          <p className="text-xs text-muted-foreground">Project role</p>
          <Select
            items={PROJECT_ROLE_ITEMS}
            value={member.role}
            onValueChange={value => {
              if (value) {
                updateMember.mutate({ userId: member.user_id, role: value as ProjectRole })
              }
            }}
          >
            <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
            <SelectContent>
              {/* The popup renders outside the panel; without the guard,
                  picking an option counts as an outside click and closes it. */}
              <PanelGuard>
                {PROJECT_ROLE_ITEMS.map(item => <SelectItem key={item.value} value={item.value}>{item.label}</SelectItem>)}
              </PanelGuard>
            </SelectContent>
          </Select>
          {updateMember.isError && (
            <p className="text-xs text-destructive">{errorMessage(updateMember.error)}</p>
          )}
          <p className="text-xs text-muted-foreground">
            Viewer can inspect resources, Member can operate workloads, and Admin can manage project access.
          </p>
        </div>
      </PanelTemplate.Section>
      <PanelTemplate.Section title="Identity">
        <dl>
          <PanelTemplate.Row label="Username">{member.username || '—'}</PanelTemplate.Row>
        </dl>
      </PanelTemplate.Section>
    </PanelTemplate>
  )
}
