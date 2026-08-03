import { useNavigate } from '@tanstack/react-router'
import { DataBodyTemplate, PageTopBar } from '@loykin/designkit'
import { DeployForm } from '@/features/serving/components/DeployForm'
import { useProjectId } from '@/lib/projectContext'

export default function ServingCreatePage() {
  const projectId = useProjectId()
  const navigate = useNavigate()

  function goToList() {
    void navigate({ to: `/projects/${projectId}/serving` })
  }

  return (
    <DataBodyTemplate
      topBar={<PageTopBar left="Serving / Deploy" />}
      title="New Service"
      description="Deploy a pipeline artifact as a managed model serving endpoint."
    >
      <DeployForm onClose={goToList} onDeployed={goToList} />
    </DataBodyTemplate>
  )
}
