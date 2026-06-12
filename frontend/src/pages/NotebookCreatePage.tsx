import { useNavigate, useSearchParams } from 'react-router-dom'
import { useCreateNotebook, useNotebookVolumes, useNotebookWorkers } from '@/features/notebooks/hooks'
import { useProjectId } from '@/lib/projectContext'
import { NotebookK8sForm } from '@/features/notebooks/components/NotebookK8sForm'

export default function NotebookCreatePage() {
  const navigate = useNavigate()
  const projectId = useProjectId()
  const [searchParams] = useSearchParams()
  const preselectedVolume = searchParams.get('volume') ?? ''

  const { mutateAsync: createNotebook, isPending: submitting, error: createError } = useCreateNotebook()
  const { data: workers = [] } = useNotebookWorkers()
  const { data: allVolumes = [] } = useNotebookVolumes()

  const releasedVolumes = allVolumes.filter(v => v.status === 'released')

  async function handleSubmit(yaml: string, volumeId?: string) {
    await createNotebook({ yaml, volumeId })
    navigate(`/projects/${projectId}/notebooks`)
  }

  return (
    <NotebookK8sForm
      workers={workers}
      releasedVolumes={releasedVolumes}
      preselectedVolume={preselectedVolume}
      onSubmit={(yaml, volumeId) => void handleSubmit(yaml, volumeId)}
      submitting={submitting}
      error={createError instanceof Error ? createError.message : undefined}
      onCancel={() => navigate(`/projects/${projectId}/notebooks`)}
    />
  )
}
