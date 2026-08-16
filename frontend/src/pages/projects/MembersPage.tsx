import { useState } from 'react'
import { Plus } from 'lucide-react'
import { DataBodyTemplate } from '@loykin/designkit'
import { DataGrid, DataGridPaginationBar } from '@loykin/gridkit'
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
import { memberColumns } from '@/features/access/memberColumns'
import { MemberDetailPanel } from '@/features/access/components/MemberDetailPanel'
import { useMembersPaged, useRemoveMember } from '@/features/access/hooks'
import type { ProjectMember } from '@/features/access/types'
import { useProjectId } from '@/lib/projectContext'
import { useNavigate } from '@/lib/router'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'

const PAGE_SIZE = 20

function MembersPageInner() {
  const projectId = useProjectId()
  const navigate = useNavigate()
  const { open } = useSidePanel()
  const [pageIndex, setPageIndex] = useState(0)
  const membersQuery = useMembersPaged(PAGE_SIZE, pageIndex * PAGE_SIZE)
  const members = membersQuery.data?.members ?? []
  const total = membersQuery.data?.total ?? 0
  const removeMember = useRemoveMember()
  const [removeTarget, setRemoveTarget] = useState<ProjectMember | null>(null)
  const [actionError, setActionError] = useState('')

  async function confirmRemove() {
    if (!removeTarget) return
    setActionError('')
    try {
      await removeMember.mutateAsync(removeTarget.user_id)
      setRemoveTarget(null)
    } catch (cause) {
      setActionError(cause instanceof Error ? cause.message : String(cause))
    }
  }

  return (
    <>
      <DataBodyTemplate
        title="Project Members"
        description="Project-specific access for Piper user accounts. Click a row to inspect or change its role."
      >
        <DataBodyTemplate.Body>
          <DataBodyTemplate.Resource
            toolbarRight={
              <Button size="sm" onClick={() => void navigate(`/projects/${projectId}/members/new`)}>
                <Plus />
                New Member
              </Button>
            }
            notice={
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
            }
          >
            <DataGrid
              data={members}
              columns={memberColumns}
              isLoading={membersQuery.isLoading}
              emptyMessage="No project members."
              tableWidthMode="fill-last"
              rowHeight={44}
              rowCursor
              onRowClick={member => open(
                <MemberDetailPanel member={member} onRemove={setRemoveTarget} />,
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

      <AlertDialog open={removeTarget != null} onOpenChange={open => { if (!open) setRemoveTarget(null) }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove this project member?</AlertDialogTitle>
            <AlertDialogDescription>
              {removeTarget?.username || 'This user'} will lose access to this project.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={removeMember.isPending}
              onClick={() => void confirmRemove()}
            >
              {removeMember.isPending ? 'Removing…' : 'Remove member'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
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
