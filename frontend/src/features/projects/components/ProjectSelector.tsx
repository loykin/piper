import { useNavigate } from '@/lib/router'
import { Check, ChevronsUpDown, FolderKanban, Plus, Trash2 } from 'lucide-react'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from '@/components/ui/sidebar'
import { useDeleteProject } from '@/features/projects/hooks'
import type { Project } from '@/features/projects/types'
import { useAuth } from '@/features/auth/context'
import { useProjectContext } from '@/features/projects/context'
import { useDeleteTarget } from '@/shared/hooks/useDeleteTarget'
import { ConfirmDialog } from '@/shared/components/ConfirmDialog'

// pkg/project/ref.go: LocalMemberID = "member-local" — every other value is a
// remote federation Member with its own separate config (including its own
// storage.url). Settings pages like Storage only ever affect the Piper
// instance actually serving this UI, so the owning Member matters here.
function memberLabel(project: Project): string {
  return project.owner_member_id === 'member-local' ? 'local' : project.owner_member_id
}

export function ProjectSelector() {
  const { projectId, projects, loading } = useProjectContext()
  const { user, capabilities } = useAuth()
  const deleteProject = useDeleteProject()
  const navigate = useNavigate()
  const { target: deleteTarget, open: deleteOpen, error: deleteError, requestDelete, cancel: cancelDelete, confirm: confirmDeleteTarget } = useDeleteTarget<Project>()

  const currentProject = projects.find(p => p.id === projectId)
  const canManageProjects = !capabilities?.authentication || user?.system_admin === true

  const handleSelect = (id: string) => {
    navigate(`/projects/${id}/schedules`, { replace: true })
  }

  const handleDelete = () => confirmDeleteTarget(async (project) => {
    await deleteProject.mutateAsync(project.id)
    const nextProject = projects.find(p => p.id !== project.id)
    if (nextProject) handleSelect(nextProject.id)
  })

  return (
    <>
      <SidebarMenu>
        <SidebarMenuItem>
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <SidebarMenuButton
                  size="lg"
                  className="data-[popup-open]:bg-sidebar-accent data-[popup-open]:text-sidebar-accent-foreground"
                  disabled={loading}
                />
              }
            >
              <div className="flex aspect-square size-8 items-center justify-center rounded-lg bg-sidebar-primary text-sidebar-primary-foreground">
                <FolderKanban className="size-4" />
              </div>
              <div className="grid flex-1 text-left text-sm leading-tight">
                <span className="truncate font-medium">
                  {currentProject?.name ?? (loading ? 'Loading projects…' : 'No project')}
                </span>
                <span className="truncate text-xs text-muted-foreground">
                  {currentProject ? `${currentProject.id} (${memberLabel(currentProject)})` : 'Select a project'}
                </span>
              </div>
              <ChevronsUpDown className="ml-auto size-4" />
            </DropdownMenuTrigger>
            <DropdownMenuContent
              className="min-w-56 rounded-lg"
              align="start"
              side="right"
              sideOffset={4}
            >
              <DropdownMenuGroup>
                <DropdownMenuLabel>Projects</DropdownMenuLabel>
                {projects.map(project => (
                  <DropdownMenuItem
                    key={project.id}
                    onClick={() => handleSelect(project.id)}
                    className="gap-2 p-2"
                  >
                    <div className="flex size-6 items-center justify-center rounded-sm border">
                      <FolderKanban className="size-3.5" />
                    </div>
                    <span className="min-w-0 flex-1 truncate">
                      {project.name}
                      <span className="ml-1.5 text-xs text-muted-foreground">({memberLabel(project)})</span>
                    </span>
                    {project.id === projectId && <Check className="size-4" />}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuGroup>
              {canManageProjects && <DropdownMenuSeparator />}
              {canManageProjects && (
                <DropdownMenuItem onClick={() => navigate('/projects/new')} className="gap-2 p-2">
                  <div className="flex size-6 items-center justify-center rounded-sm border bg-background">
                    <Plus className="size-3.5" />
                  </div>
                  <span className="font-medium text-muted-foreground">Create project</span>
                </DropdownMenuItem>
              )}
              {canManageProjects && currentProject && currentProject.id !== 'default' && projects.length > 1 && (
                <DropdownMenuItem
                  onClick={() => requestDelete(currentProject)}
                  className="gap-2 p-2 text-destructive"
                >
                  <div className="flex size-6 items-center justify-center rounded-sm border border-destructive/30">
                    <Trash2 className="size-3.5" />
                  </div>
                  <span className="font-medium">Delete current project</span>
                </DropdownMenuItem>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
        </SidebarMenuItem>
      </SidebarMenu>

      <ConfirmDialog
        open={deleteOpen}
        onCancel={cancelDelete}
        verb="Delete"
        noun="project"
        description={`"${deleteTarget?.name}" and all its project-scoped data will be permanently deleted. This action cannot be undone.`}
        error={deleteError}
        pending={deleteProject.isPending}
        onConfirm={() => void handleDelete()}
      />
    </>
  )
}
