export interface AuthUser {
  id: string
  username: string
  system_admin: boolean
  disabled?: boolean
}

export interface AuthCapabilities {
  authentication: boolean
  login_routes: boolean
  login_mode: '' | 'password' | 'redirect'
  login_url: string
  user_directory: boolean
  user_management: boolean
  project_member_management: boolean
}

export interface LoginRequest {
  username: string
  password: string
}
