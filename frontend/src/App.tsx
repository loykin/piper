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
import { CalendarClock, History, Server, Cpu, BookOpen, HardDrive, Database, GitBranch } from 'lucide-react'
import RunDetailPage from '@/pages/RunDetailPage'
import WorkflowsPage from '@/pages/WorkflowsPage'
import WorkflowCreatePage from '@/pages/WorkflowCreatePage'
import HistoryPage from '@/pages/HistoryPage'
import ScheduleDetailPage from '@/pages/ScheduleDetailPage'
import PipelineEditorPage from '@/pages/PipelineEditorPage'
import PipelinesListPage from '@/pages/PipelinesListPage'
import ServingPage from '@/pages/ServingPage'
import ServingDetailPage from '@/pages/ServingDetailPage'
import ServingHistoryPage from '@/pages/ServingHistoryPage'
import WorkersPage from '@/pages/WorkersPage'
import NotebooksPage from '@/pages/NotebooksPage'
import NotebookCreatePage from '@/pages/NotebookCreatePage'
import NotebookDetailPage from '@/pages/NotebookDetailPage'
import NotebookVolumesPage from '@/pages/NotebookVolumesPage'
import StoragePage from '@/pages/StoragePage'

const navGroups = [
  {
    label: 'Pipelines',
    items: [
      { id: 'pipelines', label: 'Templates',  icon: GitBranch,     to: '/pipelines' },
      { id: 'schedules', label: 'Schedules',  icon: CalendarClock, to: '/schedules' },
      { id: 'history',   label: 'History',    icon: History,       to: '/history' },
    ],
  },
  {
    label: 'Service',
    items: [
      { id: 'serving',         label: 'Serving',   icon: Server,    to: '/serving' },
      { id: 'serving-history', label: 'History',   icon: History,   to: '/serving/history' },
    ],
  },
  {
    label: 'Development',
    items: [
      { id: 'notebooks', label: 'Notebooks', icon: BookOpen,   to: '/notebooks' },
      { id: 'notebook-volumes', label: 'Volumes', icon: HardDrive, to: '/notebook-volumes' },
    ],
  },
  {
    label: 'Infrastructure',
    items: [
      { id: 'workers', label: 'Workers', icon: Cpu, to: '/workers' },
      { id: 'storage', label: 'Storage', icon: Database, to: '/storage' },
    ],
  },
]

function AppSidebar() {
  const location = useLocation()
  const navigate = useNavigate()

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
      </SidebarHeader>

      <SidebarContent>
        {navGroups.map((group) => (
          <SidebarGroup key={group.label}>
            <SidebarGroupLabel>{group.label}</SidebarGroupLabel>
            <SidebarGroupContent>
              <SidebarMenu>
                {group.items.map((item) => {
                  const isActive = location.pathname === item.to ||
                    (item.to !== '/history' && item.to !== '/pipelines' && location.pathname.startsWith(item.to)) ||
                    (item.to === '/pipelines' && location.pathname === '/pipelines')
                  return (
                    <SidebarMenuItem key={item.id}>
                      <SidebarMenuButton isActive={isActive} onClick={() => navigate(item.to)}>
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
        <div className="px-2 py-1 text-xs text-muted-foreground">v0.1.0</div>
      </SidebarFooter>
    </>
  )
}

export default function App() {
  return (
    <div className="h-screen dark">
      <TooltipProvider>
        <SidebarProvider>
          <Sidebar>
            <AppSidebar />
            <SidebarRail />
          </Sidebar>
          <SidebarInset>
            <div className="h-full overflow-y-auto">
              <Routes>
                <Route path="/" element={<Navigate to="/schedules" replace />} />
                <Route path="/schedules" element={<WorkflowsPage />} />
                <Route path="/schedules/create" element={<WorkflowCreatePage />} />
                <Route path="/schedules/:id" element={<ScheduleDetailPage />} />
                <Route path="/pipelines" element={<PipelinesListPage />} />
                <Route path="/pipelines/editor" element={<PipelineEditorPage />} />
                <Route path="/history" element={<HistoryPage />} />
                <Route path="/serving" element={<ServingPage />} />
                <Route path="/serving/history" element={<ServingHistoryPage />} />
                <Route path="/serving/:name" element={<ServingDetailPage />} />
                <Route path="/runs/:id" element={<RunDetailPage />} />
                <Route path="/workers" element={<WorkersPage />} />
                <Route path="/notebooks" element={<NotebooksPage />} />
                <Route path="/notebooks/create" element={<NotebookCreatePage />} />
                <Route path="/notebooks/:name" element={<NotebookDetailPage />} />
                <Route path="/notebooks/:name/promote" element={<Navigate to="/pipelines/editor" replace />} />
                <Route path="/notebook-volumes" element={<NotebookVolumesPage />} />
                <Route path="/storage" element={<StoragePage />} />
                {/* Legacy redirects */}
                <Route path="/services" element={<Navigate to="/serving" replace />} />
                <Route path="/pipelines/create" element={<Navigate to="/pipelines/editor" replace />} />
                <Route path="/run-history" element={<Navigate to="/history" replace />} />
              </Routes>
            </div>
          </SidebarInset>
        </SidebarProvider>
      </TooltipProvider>
    </div>
  )
}
