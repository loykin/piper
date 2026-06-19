import { projectApi } from '@/lib/api'
import type { Viewer, OpenViewerRequest } from './types'

export async function openViewer(
  projectId: string,
  runId: string,
  step: string,
  artifact: string,
  req: OpenViewerRequest,
): Promise<Viewer> {
  return projectApi(projectId).post<Viewer>(
    `/runs/${runId}/artifacts/${step}/${artifact}/view`,
    req,
  )
}

export async function listViewers(projectId: string): Promise<Viewer[]> {
  return projectApi(projectId).get<Viewer[]>('/viewers')
}

export async function getViewer(projectId: string, id: string): Promise<Viewer> {
  return projectApi(projectId).get<Viewer>(`/viewers/${id}`)
}

export async function stopViewer(projectId: string, id: string): Promise<void> {
  await projectApi(projectId).post(`/viewers/${id}/stop`, {})
}
