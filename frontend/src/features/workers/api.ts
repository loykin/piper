export type { Worker, WorkerPodPolicy } from './types'
import type { Worker, WorkerPodPolicy } from './types'
import { api } from '@/lib/api'

export async function listWorkers(): Promise<Worker[]> {
  const data = await api.get<Worker[]>('/api/workers')
  return Array.isArray(data) ? data : []
}

export async function listWorkerPodPolicies(): Promise<WorkerPodPolicy[]> {
  const data = await api.get<WorkerPodPolicy[]>('/api/notebook-workers/pod-policies')
  return Array.isArray(data) ? data : []
}

export async function getWorkerPodPolicy(workerId: string): Promise<WorkerPodPolicy | null> {
  try {
    return await api.get<WorkerPodPolicy>(`/api/notebook-workers/${workerId}/pod-policy`)
  } catch (err: unknown) {
    if (err && typeof err === 'object' && 'status' in err && (err as { status: number }).status === 404) {
      return null
    }
    throw err
  }
}

export async function setWorkerPodPolicy(workerId: string, yaml: string): Promise<WorkerPodPolicy> {
  return api.put<WorkerPodPolicy>(`/api/notebook-workers/${workerId}/pod-policy`, { yaml })
}

export async function deleteWorkerPodPolicy(workerId: string): Promise<void> {
  await api.delete(`/api/notebook-workers/${workerId}/pod-policy`)
}
