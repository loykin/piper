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
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
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
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { useCreateProject, useDeleteProject } from '@/features/projects/hooks'
import { useProjectContext } from '@/lib/projectContext'

export function ProjectSelector() {
  const { projectId, projects, loading } = useProjectContext()
  const createProject = useCreateProject()
  const deleteProject = useDeleteProject()
  const navigate = useNavigate()

  const [open, setOpen] = useState(false)
  const [newId, setNewId] = useState('')
  const [newName, setNewName] = useState('')
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [deleteError, setDeleteError] = useState('')

  const currentProject = projects.find(p => p.id === projectId)

  const handleSelect = (id: string) => {
    navigate(`/projects/${id}/schedules`, { replace: true })
  }

  const handleCreate = async () => {
    if (!newId.trim() || !newName.trim()) return
    const p = await createProject.mutateAsync({ id: newId.trim(), name: newName.trim() })
    navigate(`/projects/${p.id}/schedules`, { replace: true })
    setOpen(false)
    setNewId('')
    setNewName('')
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
              <DropdownMenuSeparator />
              <DropdownMenuItem onClick={() => setOpen(true)} className="gap-2 p-2">
                <div className="flex size-6 items-center justify-center rounded-sm border bg-background">
                  <Plus className="size-3.5" />
                </div>
                <span className="font-medium text-muted-foreground">Create project</span>
              </DropdownMenuItem>
              {currentProject && currentProject.id !== 'default' && projects.length > 1 && (
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

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>Create project</DialogTitle>
          </DialogHeader>
          <div className="grid gap-3 py-2">
            <div className="grid gap-1.5">
              <Label htmlFor="proj-id">ID</Label>
              <Input
                id="proj-id"
                placeholder="my-project"
                value={newId}
                onChange={e => setNewId(e.target.value)}
              />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="proj-name">Name</Label>
              <Input
                id="proj-name"
                placeholder="My Project"
                value={newName}
                onChange={e => setNewName(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && void handleCreate()}
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>Cancel</Button>
            <Button
              onClick={() => void handleCreate()}
              disabled={!newId.trim() || !newName.trim() || createProject.isPending}
            >
              Create
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

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
