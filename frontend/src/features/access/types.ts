export interface User {
  id: string
  username: string
  system_admin: boolean
  disabled: boolean
}

export interface CreateUserRequest {
  username: string
  password: string
  system_admin: boolean
}

export type ProjectRole = 'viewer' | 'member' | 'admin'

export interface MemberCandidate {
  username: string
}

export interface ProjectMember {
  project_id: string
  user_id: string
  username?: string
  role: ProjectRole
}
