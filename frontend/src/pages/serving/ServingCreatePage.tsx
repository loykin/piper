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
      topBar={<PageTopBar left="Serving" />}
      title="New Service"
    >
      <DataBodyTemplate.Body>
        <DeployForm onClose={goToList} onDeployed={goToList} />
      </DataBodyTemplate.Body>
    </DataBodyTemplate>
  )
}
