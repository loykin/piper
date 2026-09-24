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

/**
 * Select items for a project role. Pass them as the Select's `items` so the
 * trigger shows "Admin", not the raw value "admin".
 */
export const PROJECT_ROLE_ITEMS: { value: ProjectRole; label: string }[] = [
  { value: 'viewer', label: 'Viewer' },
  { value: 'member', label: 'Member' },
  { value: 'admin', label: 'Admin' },
]

export interface MemberCandidate {
  username: string
}

export interface ProjectMember {
  project_id: string
  user_id: string
  username?: string
  role: ProjectRole
}
