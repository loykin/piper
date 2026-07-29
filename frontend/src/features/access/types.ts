export interface User {
  id: string
  email: string
  display_name?: string
  system_admin: boolean
  disabled: boolean
}

export interface CreateUserRequest {
  email: string
  password: string
  system_admin: boolean
}

export type ProjectRole = 'viewer' | 'member' | 'admin'

export interface ProjectMember {
  project_id: string
  user_id: string
  role: ProjectRole
}
