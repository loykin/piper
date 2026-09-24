/* eslint-disable react-refresh/only-export-components */
import { createContext, useContext, type ReactNode } from 'react'
import { useLocation } from '@/lib/router'
import { useAuth } from '@/features/auth/context'
import { useProjects } from '@/features/projects/hooks'
import type { Project } from '@/features/projects/types'

interface ProjectContextValue {
  projectId: string
  projects: Project[]
  loading: boolean
}

const ProjectContext = createContext<ProjectContextValue>({
  projectId: '',
  projects: [],
  loading: true,
})

export function ProjectProvider({ children }: { children: ReactNode }) {
  const location = useLocation()
  const { user, capabilities, loading: authLoading } = useAuth()
  const loginRequired = capabilities?.authentication &&
    capabilities.login_routes &&
    !user
  const { data: projects = [], isLoading } = useProjects(!authLoading && !loginRequired)
  const [, projectsSegment, routeProjectId] = location.pathname.split('/')
  const projectIdFromRoute = projectsSegment === 'projects' ? routeProjectId : undefined
  const fallbackProject = projects.find(project => project.id === 'default') ?? projects[0]
  const projectId = projectIdFromRoute ?? fallbackProject?.id ?? ''

  return (
    <ProjectContext.Provider value={{ projectId, projects, loading: authLoading || isLoading }}>
      {children}
    </ProjectContext.Provider>
  )
}

export function useProjectId(): string {
  return useContext(ProjectContext).projectId
}

export function useProjectContext() {
  return useContext(ProjectContext)
}
