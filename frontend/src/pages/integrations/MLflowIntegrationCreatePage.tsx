import { useNavigate } from '@/lib/router'
import { useState } from 'react'
import { DataBodyTemplate, PageTopBar } from '@loykin/designkit'
import { MLflowIntegrationForm } from '@/features/mlflow/components/MLflowIntegrationForm'
import { useCreateMLflowIntegration } from '@/features/mlflow/hooks'
import { useProjectId } from '@/features/projects/context'
import { errorMessage } from '@/lib/format'
import { PageCrumbs } from '@/shared/components/PageCrumbs'

export default function MLflowIntegrationCreatePage() {
  const projectId = useProjectId(); const navigate = useNavigate(); const create = useCreateMLflowIntegration(); const [error, setError] = useState(''); const listPath = `/projects/${projectId}/integrations/mlflow`
  return <DataBodyTemplate topBar={<PageTopBar left={<PageCrumbs items={['Infrastructure', { label: 'MLflow Integrations', to: listPath }, 'New']} />} />} title="New MLflow Integration" description="Connect this project to an MLflow Tracking Server."><DataBodyTemplate.Group layout="stacked" title="Connection" description="Credentials remain write-only and are referenced by name."><MLflowIntegrationForm busy={create.isPending} error={error} onCancel={() => void navigate(listPath)} onSubmit={async value => { setError(''); try { await create.mutateAsync(value); void navigate(listPath) } catch (cause) { setError(errorMessage(cause)) } }} /></DataBodyTemplate.Group></DataBodyTemplate>
}
