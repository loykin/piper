/* eslint-disable react-refresh/only-export-components */
import { lazy, Suspense, useEffect } from 'react'
import { createRootRoute, createRoute, createRouter, Navigate, Outlet, RouterProvider } from '@tanstack/react-router'
import { useLocation, useNavigate } from '@/lib/router'
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
  SidebarMenuSub,
  SidebarMenuSubItem,
  SidebarMenuSubButton,
  SidebarInset,
  SidebarRail,
} from '@/components/ui/sidebar'
import { TooltipProvider } from '@/components/ui/tooltip'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger, DropdownMenuCheckboxItem } from '@/components/ui/dropdown-menu'
import { CalendarClock, History, Server, Cpu, BookOpen, HardDrive, Database, GitBranch, FlaskConical, LogOut, ChevronsUpDown, Moon, Sun, ShieldCheck, Boxes, ChevronRight, KeyRound } from 'lucide-react'
import { Collapsible, CollapsibleTrigger, CollapsibleContent } from '@/components/ui/collapsible'
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
const RunDetailPage       = lazy(() => import('@/pages/pipelines/RunDetailPage'))
const WorkflowsPage       = lazy(() => import('@/pages/schedules/WorkflowsPage'))
const WorkflowCreatePage  = lazy(() => import('@/pages/schedules/WorkflowCreatePage'))
const ScheduleDetailPage  = lazy(() => import('@/pages/schedules/ScheduleDetailPage'))
const ServingPage         = lazy(() => import('@/pages/serving/ServingPage'))
const ServingHistoryPage  = lazy(() => import('@/pages/serving/ServingHistoryPage'))
const SecretsPage         = lazy(() => import('@/pages/secrets/SecretsPage'))
const WorkersPage           = lazy(() => import('@/pages/system/WorkersPage'))
const StoragePage           = lazy(() => import('@/pages/system/StoragePage'))
const PodPoliciesPage       = lazy(() => import('@/pages/kubernetes/PodPoliciesPage'))
const PodPoliciesCreatePage = lazy(() => import('@/pages/kubernetes/PodPoliciesCreatePage'))

type NavSubItem = {
  id: string
  label: string
  to: string
  exact?: boolean
  system?: boolean
}

type NavItem = {
  id: string
  label: string
  icon: React.ComponentType
  to?: string
  exact?: boolean
  /** System-level route — enabled even without a selected project. */
  system?: boolean
  /** Nested sub-items rendered with SidebarMenuSub. */
  children?: NavSubItem[]
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
        { id: 'secrets',     label: 'Secrets',     icon: KeyRound,      to: `${base}/secrets` },
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
        { id: 'workers', label: 'Workers', icon: Cpu,      to: `/workers`, system: true },
        { id: 'storage', label: 'Storage', icon: Database, to: `${base}/storage` },
        {
          id: 'kubernetes',
          label: 'Kubernetes',
          icon: Boxes,
          system: true,
          children: [
            { id: 'pod-policies', label: 'Pod Policies', to: `/kubernetes/pod-policies`, system: true },
          ],
        },
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
                  if (item.children) {
                    const anyChildActive = item.children.some(c =>
                      location.pathname === c.to ||
                      (c.to !== '/workers' && location.pathname.startsWith(c.to + '/'))
                    )
                    return (
                      <Collapsible key={item.id} defaultOpen className="group/collapsible">
                        <SidebarMenuItem>
                          <CollapsibleTrigger
                            render={
                              <SidebarMenuButton
                                isActive={anyChildActive}
                                disabled={!projectId && !item.system}
                              />
                            }
                          >
                            <item.icon />
                            <span>{item.label}</span>
                            <ChevronRight className="ml-auto transition-transform duration-200 group-data-open/collapsible:rotate-90" />
                          </CollapsibleTrigger>
                          <CollapsibleContent>
                            <SidebarMenuSub className="mr-0 pr-0">
                              {item.children.map(child => {
                                const childActive = child.exact
                                  ? location.pathname === child.to
                                  : location.pathname === child.to ||
                                    (child.to !== '/workers' && location.pathname.startsWith(child.to + '/'))
                                return (
                                  <SidebarMenuSubItem key={child.id}>
                                    <SidebarMenuSubButton
                                      render={<button type="button" />}
                                      isActive={childActive}
                                      onClick={() => {
                                        if (projectId || child.system) void navigate(child.to)
                                      }}
                                      aria-disabled={!projectId && !child.system}
                                    >
                                      {child.label}
                                    </SidebarMenuSubButton>
                                  </SidebarMenuSubItem>
                                )
                              })}
                            </SidebarMenuSub>
                          </CollapsibleContent>
                        </SidebarMenuItem>
                      </Collapsible>
                    )
                  }

                  const isActive = item.exact
                    ? location.pathname === item.to
                    : location.pathname === item.to ||
                      (item.to !== '/workers' && item.to != null && location.pathname.startsWith(item.to + '/'))
                  return (
                    <SidebarMenuItem key={item.id}>
                      <SidebarMenuButton
                        isActive={isActive}
                        onClick={() => item.to && navigate(item.to)}
                        disabled={!projectId && !item.system}
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

function RoutedContent() {
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

  return <Outlet />
}

function AppLayout() {
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
                    <RoutedContent />
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

function RootRedirect() {
  const { projectId, loading } = useProjectContext()
  if (loading) return null
  return <RedirectTo to={projectId ? `/projects/${projectId}/schedules` : '/workers'} />
}

function ProjectScopedFallback() {
  const { projectId } = useProjectContext()
  return <RedirectTo to={projectId ? `/projects/${projectId}/schedules` : '/'} />
}

function LegacyProjectRedirect({ to }: { to: string }) {
  const { projectId } = useProjectContext()
  return <RedirectTo to={projectId ? `/projects/${projectId}/${to}` : '/'} />
}

function RedirectTo({ to }: { to: string }) {
  const navigate = useNavigate()
  useEffect(() => {
    void navigate(to, { replace: true })
  }, [navigate, to])
  return null
}

const rootRoute = createRootRoute({
  component: AppLayout,
})

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: RootRedirect,
})

const projectRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'projects/$project_id',
})

const projectRoutes = [
  createRoute({ getParentRoute: () => projectRoute, path: 'schedules', component: WorkflowsPage }),
  createRoute({ getParentRoute: () => projectRoute, path: 'schedules/create', component: WorkflowCreatePage }),
  createRoute({ getParentRoute: () => projectRoute, path: 'schedules/$id', component: ScheduleDetailPage }),
  createRoute({ getParentRoute: () => projectRoute, path: 'pipelines', component: PipelinesListPage }),
  createRoute({ getParentRoute: () => projectRoute, path: 'pipelines/editor', component: PipelineEditorPage }),
  createRoute({ getParentRoute: () => projectRoute, path: 'history', component: HistoryPage }),
  createRoute({ getParentRoute: () => projectRoute, path: 'runs/$id', component: RunDetailPage }),
  createRoute({ getParentRoute: () => projectRoute, path: 'experiments', component: ExperimentsPage }),
  createRoute({ getParentRoute: () => projectRoute, path: 'secrets', component: SecretsPage }),
  createRoute({ getParentRoute: () => projectRoute, path: 'serving', component: ServingPage }),
  createRoute({ getParentRoute: () => projectRoute, path: 'serving/history', component: ServingHistoryPage }),
  createRoute({ getParentRoute: () => projectRoute, path: 'notebooks', component: NotebooksPage }),
  createRoute({ getParentRoute: () => projectRoute, path: 'notebooks/create', component: NotebookCreatePage }),
  createRoute({ getParentRoute: () => projectRoute, path: 'notebook-volumes', component: NotebookVolumesPage }),
  createRoute({ getParentRoute: () => projectRoute, path: 'storage', component: StoragePage }),
  createRoute({ getParentRoute: () => projectRoute, path: '$', component: ProjectScopedFallback }),
]

const legacyProjectRoutes = [
  createRoute({ getParentRoute: () => rootRoute, path: 'schedules', component: () => <LegacyProjectRedirect to="schedules" /> }),
  createRoute({ getParentRoute: () => rootRoute, path: 'pipelines', component: () => <LegacyProjectRedirect to="pipelines" /> }),
  createRoute({ getParentRoute: () => rootRoute, path: 'history', component: () => <LegacyProjectRedirect to="history" /> }),
  createRoute({ getParentRoute: () => rootRoute, path: 'serving', component: () => <LegacyProjectRedirect to="serving" /> }),
  createRoute({ getParentRoute: () => rootRoute, path: 'notebooks', component: () => <LegacyProjectRedirect to="notebooks" /> }),
  createRoute({ getParentRoute: () => rootRoute, path: 'notebook-volumes', component: () => <LegacyProjectRedirect to="notebook-volumes" /> }),
  createRoute({ getParentRoute: () => rootRoute, path: 'storage', component: () => <LegacyProjectRedirect to="storage" /> }),
]

const routeTree = rootRoute.addChildren([
  indexRoute,
  projectRoute.addChildren(projectRoutes),
  createRoute({ getParentRoute: () => rootRoute, path: 'workers', component: WorkersPage }),
  createRoute({ getParentRoute: () => rootRoute, path: 'kubernetes/pod-policies', component: PodPoliciesPage }),
  createRoute({ getParentRoute: () => rootRoute, path: 'kubernetes/pod-policies/new', component: PodPoliciesCreatePage }),
  createRoute({ getParentRoute: () => rootRoute, path: 'login', component: LoginPage }),
  ...legacyProjectRoutes,
  createRoute({ getParentRoute: () => rootRoute, path: '$', component: () => <RedirectTo to="/" /> }),
])

export const router = createRouter({
  routeTree,
  basepath: '/ui',
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}

export default function App() {
  return <RouterProvider router={router} />
}
