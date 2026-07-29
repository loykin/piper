import { Trash2, X } from 'lucide-react'
import {
  PanelTemplate,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { useUpdateMember } from '../hooks'
import type { ProjectMember, ProjectRole } from '../types'

interface Props {
  member: ProjectMember
  onRemove: (member: ProjectMember) => void
}

export function MemberDetailPanel({ member, onRemove }: Props) {
  const { close } = useSidePanel()
  const updateMember = useUpdateMember()

  return (
    <PanelTemplate
      eyebrow="Project membership"
      title={member.username || 'Unknown user'}
      actions={
        <div className="flex items-center gap-1">
          <IconButton
            icon={<Trash2 />}
            label={`Remove ${member.username || 'member'}`}
            className="text-destructive hover:bg-destructive/10"
            onClick={() => {
              onRemove(member)
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
      <PanelTemplate.Section title="Access">
        <div className="space-y-1.5">
          <p className="text-xs text-muted-foreground">Project role</p>
          <Select
            value={member.role}
            onValueChange={value => {
              if (value) {
                updateMember.mutate({ userId: member.user_id, role: value as ProjectRole })
              }
            }}
          >
            <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="viewer">Viewer</SelectItem>
              <SelectItem value="member">Member</SelectItem>
              <SelectItem value="admin">Admin</SelectItem>
            </SelectContent>
          </Select>
          <p className="text-xs text-muted-foreground">
            Viewer can inspect resources, Member can operate workloads, and Admin can manage project access.
          </p>
        </div>
      </PanelTemplate.Section>
      <PanelTemplate.Section title="Identity">
        <dl>
          <div>
            <dt className="text-xs text-muted-foreground">Username</dt>
            <dd className="mt-0.5 text-sm font-medium">{member.username || '—'}</dd>
          </div>
        </dl>
      </PanelTemplate.Section>
    </PanelTemplate>
  )
}
