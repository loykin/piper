import { Routes, Route, NavLink } from 'react-router-dom'
import RunsPage from './pages/RunsPage'
import RunDetailPage from './pages/RunDetailPage'

export default function App() {
  return (
    <div className="min-h-screen bg-gray-950 text-gray-100">
      {/* Top navigation */}
      <header className="border-b border-gray-800 bg-gray-900">
        <div className="mx-auto max-w-7xl flex items-center gap-6 px-6 py-3">
          <span className="text-lg font-bold text-indigo-400 tracking-tight">⚡ piper</span>
          <NavLink
            to="/"
            className={({ isActive }) =>
              `text-sm font-medium transition-colors ${isActive ? 'text-white' : 'text-gray-400 hover:text-gray-200'}`
            }
          >
            Runs
          </NavLink>
        </div>
      </header>

      <main className="mx-auto max-w-7xl px-6 py-8">
        <Routes>
          <Route path="/" element={<RunsPage />} />
          <Route path="/runs/:id" element={<RunDetailPage />} />
        </Routes>
      </main>
    </div>
  )
}
