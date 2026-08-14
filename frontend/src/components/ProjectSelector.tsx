import { useState } from 'react'
import { useNavigate } from '@/lib/router'
import { Check, ChevronsUpDown, FolderKanban, Plus, Trash2 } from 'lucide-react'
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
import { useAuth } from '@/features/auth/context'
import { useProjectContext } from '@/lib/projectContext'

export function ProjectSelector() {
  const { projectId, projects, loading } = useProjectContext()
  const { user, capabilities } = useAuth()
  const deleteProject = useDeleteProject()
  const navigate = useNavigate()

  const [deleteOpen, setDeleteOpen] = useState(false)
  const [deleteError, setDeleteError] = useState('')

  const currentProject = projects.find(p => p.id === projectId)
  const canManageProjects = !capabilities?.authentication || user?.system_admin === true

  const handleSelect = (id: string) => {
    navigate(`/projects/${id}/schedules`, { replace: true })
  }

  const handleDelete = async () => {
    if (!currentProject || currentProject.id === 'default' || projects.length <= 1) return
    setDeleteError('')
    try {
      await deleteProject.mutateAsync(currentProject.id)
      const nextProject = projects.find(project => project.id !== currentProject.id)
      setDeleteOpen(false)
      if (nextProject) handleSelect(nextProject.id)
    } catch (error) {
      setDeleteError(error instanceof Error ? error.message : String(error))
    }
  }

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
                  {currentProject?.id ?? 'Select a project'}
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
                    <span className="min-w-0 flex-1 truncate">{project.name}</span>
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
                  onClick={() => setDeleteOpen(true)}
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

      <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete {currentProject?.name}?</AlertDialogTitle>
            <AlertDialogDescription>
              This permanently deletes the project and its project-scoped data. This action cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          {deleteError && <p className="text-sm text-destructive">{deleteError}</p>}
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => void handleDelete()}
              disabled={deleteProject.isPending}
            >
              {deleteProject.isPending ? 'Deleting…' : 'Delete project'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
