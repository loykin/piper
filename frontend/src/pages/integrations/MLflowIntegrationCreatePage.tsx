import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { DataBodyTemplate, PageTopBar } from '@loykin/designkit'
import { MLflowIntegrationForm } from '@/features/mlflow/components/MLflowIntegrationForm'
import { useCreateMLflowIntegration } from '@/features/mlflow/hooks'
import { useProjectId } from '@/lib/projectContext'

export default function MLflowIntegrationCreatePage() {
  const projectId = useProjectId(); const navigate = useNavigate(); const create = useCreateMLflowIntegration(); const [error, setError] = useState(''); const listPath = `/projects/${projectId}/integrations/mlflow`
  return <DataBodyTemplate topBar={<PageTopBar left="MLflow Integrations / New" />} title="New MLflow Integration" description="Connect this project to an MLflow Tracking Server."><DataBodyTemplate.Group layout="stacked" title="Connection" description="Credentials remain write-only and are referenced by name."><MLflowIntegrationForm busy={create.isPending} error={error} onCancel={() => void navigate({ to: listPath })} onSubmit={async value => { setError(''); try { await create.mutateAsync(value); void navigate({ to: listPath }) } catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)) } }} /></DataBodyTemplate.Group></DataBodyTemplate>
}
