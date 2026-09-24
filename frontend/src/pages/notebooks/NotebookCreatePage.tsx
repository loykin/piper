import { DataBodyTemplate, PageTopBar } from '@loykin/designkit'
import { NotebookLaunchForm, type NotebookRuntime } from '@/features/notebooks/components/NotebookLaunchForm'
import { useCreateNotebook, useNotebookVolumes } from '@/features/notebooks/hooks'
import { useProjectId } from '@/features/projects/context'
import { useSystemSettings } from '@/features/system/hooks'
import { errorMessage } from '@/lib/format'
import { useNavigate, useSearchParams } from '@/lib/router'
import { PageCrumbs } from '@/shared/components/PageCrumbs'
import { toneBadge } from '@/shared/status'

const RUNTIME_LABEL: Record<NotebookRuntime, string> = { k8s: 'Kubernetes', docker: 'Docker', baremetal: 'Bare-metal' }
const RUNTIME_TONE = { k8s: toneBadge.info, docker: toneBadge.starting, baremetal: toneBadge.attention } as const

export default function NotebookCreatePage() {
  const navigate = useNavigate()
  const projectId = useProjectId()
  const [searchParams] = useSearchParams()
  const preselectedVolume = searchParams.get('volume') ?? ''
  const listPath = `/projects/${projectId}/notebooks`

  const { mutateAsync: createNotebook, isPending: submitting, error: createError } = useCreateNotebook()
  const { data: allVolumes = [] } = useNotebookVolumes()
  const releasedVolumes = allVolumes.filter(v => v.status === 'released')

  // This Piper installation owns exactly one runtime (baremetal, docker, or
  // k8s) for direct in-process execution — the notebook always launches on it.
  const { data: systemSettings } = useSystemSettings()
  const runtime = (systemSettings?.runtime?.type as NotebookRuntime | undefined) || 'baremetal'

  async function handleSubmit(yaml: string, volumeId?: string) {
    await createNotebook({ yaml, volumeId })
    navigate(listPath)
  }

  return (
    <DataBodyTemplate
      topBar={<PageTopBar left={<PageCrumbs items={['Development', { label: 'Notebooks', to: listPath }, 'Launch']} />} />}
      title="Launch Notebook Server"
      description={<span className={`rounded px-2 py-0.5 text-xs font-medium ${RUNTIME_TONE[runtime]}`}>{RUNTIME_LABEL[runtime]}</span>}
    >
      <NotebookLaunchForm
        runtime={runtime}
        releasedVolumes={releasedVolumes}
        preselectedVolume={preselectedVolume}
        onSubmit={(yaml, volumeId) => void handleSubmit(yaml, volumeId).catch(() => undefined)}
        submitting={submitting}
        error={createError ? errorMessage(createError) : undefined}
        onCancel={() => navigate(listPath)}
      />
    </DataBodyTemplate>
  )
}
