export interface Project {
  id: string
  name: string
  description: string
  created_at: string
  updated_at: string
}

export interface CreateProjectRequest {
  id: string
  name: string
  description?: string
}
