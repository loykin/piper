import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Check, ChevronsUpDown, FolderKanban, Plus } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  DropdownMenu,
  DropdownMenuContent,
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
import { useCreateProject } from '@/features/projects/hooks'
import { useProjectContext } from '@/lib/projectContext'

export function ProjectSelector() {
  const { projectId, projects, loading } = useProjectContext()
  const createProject = useCreateProject()
  const navigate = useNavigate()

  const [open, setOpen] = useState(false)
  const [newId, setNewId] = useState('')
  const [newName, setNewName] = useState('')

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
              <DropdownMenuSeparator />
              <DropdownMenuItem onClick={() => setOpen(true)} className="gap-2 p-2">
                <div className="flex size-6 items-center justify-center rounded-sm border bg-background">
                  <Plus className="size-3.5" />
                </div>
                <span className="font-medium text-muted-foreground">Create project</span>
              </DropdownMenuItem>
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
    </>
  )
}
