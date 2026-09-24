import { useNavigate } from '@/lib/router'
import { DataBodyTemplate, PageTopBar } from '@loykin/designkit'
import { DeployForm } from '@/features/serving/components/DeployForm'
import { useProjectId } from '@/features/projects/context'
import { PageCrumbs } from '@/shared/components/PageCrumbs'

export default function ServingCreatePage() {
  const projectId = useProjectId()
  const navigate = useNavigate()

  function goToList() {
    void navigate(`/projects/${projectId}/serving`)
  }

  return (
    <DataBodyTemplate
      topBar={<PageTopBar left={<PageCrumbs items={['Service', { label: 'Serving', to: `/projects/${projectId}/serving` }, 'Deploy']} />} />}
      title="New Service"
      description="Deploy a pipeline artifact as a managed model serving endpoint."
    >
      <DeployForm onClose={goToList} onDeployed={goToList} />
    </DataBodyTemplate>
  )
}
