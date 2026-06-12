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
import { CalendarClock, History, Server, Cpu, BookOpen, HardDrive, Database, GitBranch, FlaskConical, LogOut } from 'lucide-react'
import { ProjectSelector } from '@/components/ProjectSelector'
import { ProjectProvider, useProjectContext } from '@/lib/projectContext'
import { AuthProvider, useAuth } from '@/features/auth/context'

const LoginPage             = lazy(() => import('@/pages/LoginPage'))
const RunDetailPage         = lazy(() => import('@/pages/RunDetailPage'))
const ExperimentsPage       = lazy(() => import('@/pages/ExperimentsPage'))
const ExperimentDetailPage  = lazy(() => import('@/pages/ExperimentDetailPage'))
const WorkflowsPage         = lazy(() => import('@/pages/WorkflowsPage'))
const WorkflowCreatePage    = lazy(() => import('@/pages/WorkflowCreatePage'))
const HistoryPage           = lazy(() => import('@/pages/HistoryPage'))
const ScheduleDetailPage    = lazy(() => import('@/pages/ScheduleDetailPage'))
const PipelineEditorPage    = lazy(() => import('@/pages/PipelineEditorPage'))
const PipelinesListPage     = lazy(() => import('@/pages/PipelinesListPage'))
const ServingPage           = lazy(() => import('@/pages/ServingPage'))
const ServingDetailPage     = lazy(() => import('@/pages/ServingDetailPage'))
const ServingHistoryPage    = lazy(() => import('@/pages/ServingHistoryPage'))
const WorkersPage           = lazy(() => import('@/pages/WorkersPage'))
const NotebooksPage         = lazy(() => import('@/pages/NotebooksPage'))
const NotebookCreatePage    = lazy(() => import('@/pages/NotebookCreatePage'))
const NotebookDetailPage    = lazy(() => import('@/pages/NotebookDetailPage'))
const NotebookVolumesPage   = lazy(() => import('@/pages/NotebookVolumesPage'))
const StoragePage           = lazy(() => import('@/pages/StoragePage'))

function navItems(projectId: string) {
  const base = `/projects/${projectId}`
  return [
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
        { id: 'serving',         label: 'Serving',  icon: Server,  to: `${base}/serving` },
        { id: 'serving-history', label: 'History',  icon: History, to: `${base}/serving/history` },
      ],
    },
    {
      label: 'Development',
      items: [
        { id: 'notebooks',        label: 'Notebooks', icon: BookOpen,  to: `${base}/notebooks` },
        { id: 'notebook-volumes', label: 'Volumes',   icon: HardDrive, to: `${base}/notebook-volumes` },
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

  return (
    <>
      <SidebarHeader>
        <div className="flex items-center gap-2 px-2 py-1">
          <div className="flex h-7 w-7 items-center justify-center rounded bg-primary text-xs font-bold text-primary-foreground">
            P
          </div>
          <div>
            <p className="text-sm font-semibold leading-none">piper</p>
            <p className="mt-0.5 text-[11px] text-muted-foreground">Pipeline Control</p>
          </div>
        </div>
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
                  const isActive = location.pathname === item.to ||
                    (item.to !== '/workers' && location.pathname.startsWith(item.to + '/')) ||
                    location.pathname.startsWith(item.to)
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
        {user && (
          <div className="flex items-center justify-between px-2 py-1">
            <div className="min-w-0">
              <p className="truncate text-xs font-medium">{user.email}</p>
              {user.system_admin && (
                <p className="text-[11px] text-muted-foreground">admin</p>
              )}
            </div>
            <button
              onClick={() => { void logout().then(() => navigate('/login')) }}
              className="ml-2 shrink-0 rounded p-1 text-muted-foreground hover:text-foreground"
              title="Sign out"
            >
              <LogOut className="h-4 w-4" />
            </button>
          </div>
        )}
        {!user && capabilities?.authentication && capabilities.login_mode !== '' && (
          <div className="px-2 py-1">
            <button
              onClick={() => navigate('/login')}
              className="text-xs text-muted-foreground hover:text-foreground"
            >
              Sign in
            </button>
          </div>
        )}
        <div className="px-2 pb-1 text-xs text-muted-foreground">v0.1.0</div>
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
          <Route path="schedules/:id" element={<ScheduleDetailPage />} />
          <Route path="pipelines" element={<PipelinesListPage />} />
          <Route path="pipelines/editor" element={<PipelineEditorPage />} />
          <Route path="history" element={<HistoryPage />} />
          <Route path="experiments" element={<ExperimentsPage />} />
          <Route path="experiments/:name" element={<ExperimentDetailPage />} />
          <Route path="serving" element={<ServingPage />} />
          <Route path="serving/history" element={<ServingHistoryPage />} />
          <Route path="serving/:name" element={<ServingDetailPage />} />
          <Route path="runs/:id" element={<RunDetailPage />} />
          <Route path="notebooks" element={<NotebooksPage />} />
          <Route path="notebooks/create" element={<NotebookCreatePage />} />
          <Route path="notebooks/:name" element={<NotebookDetailPage />} />
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
            <SidebarProvider>
              <Sidebar>
                <AppSidebar />
                <SidebarRail />
              </Sidebar>
              <SidebarInset>
                <div className="h-full overflow-y-auto">
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
