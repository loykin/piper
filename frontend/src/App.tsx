import { lazy, Suspense } from 'react'
import { useLocation, useNavigate, Routes, Route, Navigate } from 'react-router-dom'
import {
  SidebarProvider,
  Sidebar,
  SidebarContent,
  SidebarHeader,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupLabel,
  SidebarGroupContent,
  SidebarMenu,
  SidebarMenuItem,
  SidebarMenuButton,
  SidebarInset,
  SidebarRail,
} from '@/components/ui/sidebar'
import { TooltipProvider } from '@/components/ui/tooltip'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger, DropdownMenuCheckboxItem } from '@/components/ui/dropdown-menu'
import { CalendarClock, History, Server, Cpu, BookOpen, HardDrive, Database, GitBranch, FlaskConical, LogOut, ChevronsUpDown, Moon, Sun, ShieldCheck } from 'lucide-react'
import { ProjectSelector } from '@/components/ProjectSelector'
import { ProjectProvider, useProjectContext } from '@/lib/projectContext'
import { AuthProvider, useAuth } from '@/features/auth/context'
import { useState } from 'react'

const LoginPage           = lazy(() => import('@/pages/LoginPage'))
const NotebooksPage       = lazy(() => import('@/pages/notebooks/NotebooksPage'))
const NotebookCreatePage  = lazy(() => import('@/pages/notebooks/NotebookCreatePage'))
const NotebookVolumesPage = lazy(() => import('@/pages/notebooks/NotebookVolumesPage'))
const PipelinesListPage   = lazy(() => import('@/pages/pipelines/PipelinesListPage'))
const PipelineEditorPage  = lazy(() => import('@/pages/pipelines/PipelineEditorPage'))
const HistoryPage         = lazy(() => import('@/pages/pipelines/HistoryPage'))
const ExperimentsPage     = lazy(() => import('@/pages/pipelines/ExperimentsPage'))
const WorkflowsPage       = lazy(() => import('@/pages/schedules/WorkflowsPage'))
const WorkflowCreatePage  = lazy(() => import('@/pages/schedules/WorkflowCreatePage'))
const ServingPage         = lazy(() => import('@/pages/serving/ServingPage'))
const ServingHistoryPage  = lazy(() => import('@/pages/serving/ServingHistoryPage'))
const WorkersPage         = lazy(() => import('@/pages/system/WorkersPage'))
const StoragePage         = lazy(() => import('@/pages/system/StoragePage'))

type NavItem = {
  id: string
  label: string
  icon: React.ComponentType
  to: string
  exact?: boolean
}

function navItems(projectId: string): { label: string; items: NavItem[] }[] {
  const base = `/projects/${projectId}`
  return [
    {
      label: 'Development',
      items: [
        { id: 'notebooks',        label: 'Notebooks', icon: BookOpen,  to: `${base}/notebooks` },
        { id: 'notebook-volumes', label: 'Volumes',   icon: HardDrive, to: `${base}/notebook-volumes` },
      ],
    },
    {
      label: 'Pipelines',
      items: [
        { id: 'pipelines',   label: 'Templates',   icon: GitBranch,     to: `${base}/pipelines` },
        { id: 'schedules',   label: 'Schedules',   icon: CalendarClock, to: `${base}/schedules` },
        { id: 'history',     label: 'History',     icon: History,       to: `${base}/history` },
        { id: 'experiments', label: 'Experiments', icon: FlaskConical,  to: `${base}/experiments` },
      ],
    },
    {
      label: 'Service',
      items: [
        { id: 'serving',         label: 'Serving',  icon: Server,  to: `${base}/serving`,         exact: true },
        { id: 'serving-history', label: 'History',  icon: History, to: `${base}/serving/history`, exact: true },
      ],
    },
    {
      label: 'Infrastructure',
      items: [
        { id: 'workers', label: 'Workers', icon: Cpu,      to: `/workers` },
        { id: 'storage', label: 'Storage', icon: Database, to: `${base}/storage` },
      ],
    },
  ]
}

function AppSidebar() {
  const location = useLocation()
  const navigate = useNavigate()
  const { projectId } = useProjectContext()
  const { user, capabilities, logout } = useAuth()
  const groups = navItems(projectId)

  const [isDark, setIsDark] = useState(() =>
    document.documentElement.classList.contains('dark')
  )

  function toggleDark(checked: boolean) {
    setIsDark(checked)
    document.documentElement.classList.toggle('dark', checked)
  }

  return (
    <>
      <SidebarHeader>
        <div className="px-1 pt-1">
          <ProjectSelector />
        </div>
      </SidebarHeader>

      <SidebarContent>
        {groups.map((group) => (
          <SidebarGroup key={group.label}>
            <SidebarGroupLabel>{group.label}</SidebarGroupLabel>
            <SidebarGroupContent>
              <SidebarMenu>
                {group.items.map((item) => {
                  const isActive = item.exact
                    ? location.pathname === item.to
                    : location.pathname === item.to ||
                      (item.to !== '/workers' && location.pathname.startsWith(item.to + '/'))
                  return (
                    <SidebarMenuItem key={item.id}>
                      <SidebarMenuButton
                        isActive={isActive}
                        onClick={() => navigate(item.to)}
                        disabled={!projectId && item.to !== '/workers'}
                      >
                        <item.icon />
                        <span>{item.label}</span>
                      </SidebarMenuButton>
                    </SidebarMenuItem>
                  )
                })}
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
        ))}
      </SidebarContent>

      <SidebarFooter>
        <SidebarMenu>
          <SidebarMenuItem>
            {user ? (
              <DropdownMenu>
                <DropdownMenuTrigger className="w-full">
                  <SidebarMenuButton size="lg" className="data-[state=open]:bg-sidebar-accent">
                    <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-sidebar-primary text-sidebar-primary-foreground text-xs font-semibold">
                      {user.email.slice(0, 2).toUpperCase()}
                    </div>
                    <div className="min-w-0 flex-1 text-left">
                      <p className="truncate text-sm font-medium">{user.email}</p>
                      {user.system_admin && (
                        <p className="truncate text-xs text-muted-foreground">System Admin</p>
                      )}
                    </div>
                    <ChevronsUpDown className="ml-auto h-4 w-4 shrink-0 text-muted-foreground" />
                  </SidebarMenuButton>
                </DropdownMenuTrigger>
                <DropdownMenuContent side="top" align="start" className="w-56">
                  <DropdownMenuLabel className="font-normal">
                    <div className="flex flex-col gap-0.5">
                      <span className="truncate text-sm font-medium">{user.email}</span>
                      {user.system_admin && (
                        <span className="flex items-center gap-1 text-xs text-muted-foreground">
                          <ShieldCheck className="h-3 w-3" /> System Admin
                        </span>
                      )}
                    </div>
                  </DropdownMenuLabel>
                  <DropdownMenuSeparator />
                  <DropdownMenuCheckboxItem
                    checked={isDark}
                    onCheckedChange={toggleDark}
                  >
                    {isDark ? <Moon className="mr-2 h-4 w-4" /> : <Sun className="mr-2 h-4 w-4" />}
                    Dark mode
                  </DropdownMenuCheckboxItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem
                    className="text-destructive focus:text-destructive"
                    onSelect={() => { void logout().then(() => navigate('/login')) }}
                  >
                    <LogOut className="mr-2 h-4 w-4" />
                    Sign out
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            ) : capabilities?.authentication && capabilities.login_mode !== '' ? (
              <SidebarMenuButton onClick={() => navigate('/login')}>
                Sign in
              </SidebarMenuButton>
            ) : null}
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
    </>
  )
}

function ProjectRoutes() {
  const location = useLocation()
  const { user, capabilities, loading } = useAuth()

  if (loading) return null
  if (
    capabilities?.authentication &&
    capabilities.login_routes &&
    !user &&
    location.pathname !== '/login'
  ) {
    return <Navigate to="/login" replace />
  }

  return (
    <Routes>
      <Route path="projects/:project_id/*" element={
        <Routes>
          <Route path="schedules" element={<WorkflowsPage />} />
          <Route path="schedules/create" element={<WorkflowCreatePage />} />
          <Route path="pipelines" element={<PipelinesListPage />} />
          <Route path="pipelines/editor" element={<PipelineEditorPage />} />
          <Route path="history" element={<HistoryPage />} />
          <Route path="experiments" element={<ExperimentsPage />} />
          <Route path="serving" element={<ServingPage />} />
          <Route path="serving/history" element={<ServingHistoryPage />} />
          <Route path="notebooks" element={<NotebooksPage />} />
          <Route path="notebooks/create" element={<NotebookCreatePage />} />
          <Route path="notebook-volumes" element={<NotebookVolumesPage />} />
          <Route path="storage" element={<StoragePage />} />
          <Route path="*" element={<Navigate to="schedules" replace />} />
        </Routes>
      } />

      {/* System routes (no project) */}
      <Route path="workers" element={<WorkersPage />} />
      <Route path="login" element={<LoginPage />} />

      {/* Root redirect */}
      <Route path="/" element={<RootRedirect />} />

      {/* Legacy redirects */}
      <Route path="schedules" element={<LegacyProjectRedirect to="schedules" />} />
      <Route path="pipelines" element={<LegacyProjectRedirect to="pipelines" />} />
      <Route path="history" element={<LegacyProjectRedirect to="history" />} />
      <Route path="serving" element={<LegacyProjectRedirect to="serving" />} />
      <Route path="notebooks" element={<LegacyProjectRedirect to="notebooks" />} />
      <Route path="notebook-volumes" element={<LegacyProjectRedirect to="notebook-volumes" />} />
      <Route path="storage" element={<LegacyProjectRedirect to="storage" />} />

      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}

function RootRedirect() {
  const { projectId, loading } = useProjectContext()
  if (loading) return null
  if (projectId) return <Navigate to={`/projects/${projectId}/schedules`} replace />
  return <Navigate to="/workers" replace />
}

function LegacyProjectRedirect({ to }: { to: string }) {
  const { projectId } = useProjectContext()
  if (projectId) return <Navigate to={`/projects/${projectId}/${to}`} replace />
  return <Navigate to="/" replace />
}

export default function App() {
  return (
    <AuthProvider>
      <ProjectProvider>
        <div className="h-screen dark">
          <TooltipProvider>
            <SidebarProvider className="h-screen">
              <Sidebar>
                <AppSidebar />
                <SidebarRail />
              </Sidebar>
              <SidebarInset>
                <div className="flex flex-col flex-1 min-h-0">
                  <Suspense fallback={<div className="py-8 text-center text-sm text-muted-foreground">Loading…</div>}>
                    <ProjectRoutes />
                  </Suspense>
                </div>
              </SidebarInset>
            </SidebarProvider>
          </TooltipProvider>
        </div>
      </ProjectProvider>
    </AuthProvider>
  )
}
