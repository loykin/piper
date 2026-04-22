import { Routes, Route, NavLink, Navigate } from 'react-router-dom'
import RunDetailPage from './pages/RunDetailPage'
import WorkflowsPage from './pages/WorkflowsPage'
import WorkflowCreatePage from './pages/WorkflowCreatePage'
import TaskHistoryPage from './pages/TaskHistoryPage'
import ScheduleDetailPage from './pages/ScheduleDetailPage'

function SidebarLink({ to, label }: { to: string; label: string }) {
  return (
    <NavLink
      to={to}
      className={({ isActive }) =>
        [
          'block rounded-lg px-3 py-2 text-sm font-medium transition-colors',
          isActive ? 'bg-indigo-600/20 text-indigo-300' : 'text-gray-400 hover:bg-gray-800 hover:text-gray-200',
        ].join(' ')
      }
    >
      {label}
    </NavLink>
  )
}

export default function App() {
  return (
    <div className="min-h-screen bg-gray-950 text-gray-100">
      <div className="mx-auto flex min-h-screen max-w-[1400px]">
        <aside className="w-64 shrink-0 border-r border-gray-800 bg-gray-900/70 px-4 py-6">
          <div className="mb-6 px-2">
            <p className="text-lg font-bold tracking-tight text-indigo-300">piper</p>
            <p className="mt-1 text-xs text-gray-500">Pipeline Control</p>
          </div>

          <nav className="space-y-1">
            <SidebarLink to="/schedules" label="Schedule" />
            <SidebarLink to="/history" label="History" />
          </nav>
        </aside>

        <main className="flex-1 px-6 py-8">
          <Routes>
            <Route path="/" element={<Navigate to="/schedules" replace />} />
            <Route path="/schedules" element={<WorkflowsPage />} />
            <Route path="/schedules/create" element={<WorkflowCreatePage />} />
            <Route path="/schedules/:id" element={<ScheduleDetailPage />} />
            <Route path="/history" element={<TaskHistoryPage />} />
            <Route path="/pipelines" element={<Navigate to="/schedules" replace />} />
            <Route path="/pipelines/create" element={<Navigate to="/schedules/create" replace />} />
            <Route path="/run-history" element={<Navigate to="/history" replace />} />
            <Route path="/runs/:id" element={<RunDetailPage />} />
          </Routes>
        </main>
      </div>
    </div>
  )
}
