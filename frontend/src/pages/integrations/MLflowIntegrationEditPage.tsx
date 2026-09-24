import { useState } from 'react'
import { DataBodyTemplate, PageTopBar } from '@loykin/designkit'
import { useParams, useNavigate } from '@/lib/router'
import { MLflowIntegrationForm } from '@/features/mlflow/components/MLflowIntegrationForm'
import { useMLflowIntegration, useUpdateMLflowIntegration } from '@/features/mlflow/hooks'
import { useProjectId } from '@/features/projects/context'
import { errorMessage } from '@/lib/format'
import { PageCrumbs } from '@/shared/components/PageCrumbs'

export default function MLflowIntegrationEditPage() {
  const { id } = useParams<{ id: string }>(); const projectId = useProjectId(); const navigate = useNavigate(); const query = useMLflowIntegration(id!); const update = useUpdateMLflowIntegration(id!); const [error, setError] = useState(''); const listPath = `/projects/${projectId}/integrations/mlflow`
  if (query.isLoading) return <DataBodyTemplate title="Loading…" />
  if (!query.data) return <DataBodyTemplate title="Integration not found" />
  return <DataBodyTemplate topBar={<PageTopBar left={<PageCrumbs items={['Infrastructure', { label: 'MLflow Integrations', to: listPath }, 'Edit']} />} />} title={`Edit ${query.data.name}`} description="Update connection and export behavior without exposing credential values."><DataBodyTemplate.Group layout="stacked" title="Connection"><MLflowIntegrationForm key={query.data.updated_at} initial={query.data} busy={update.isPending} error={error} onCancel={() => void navigate(listPath)} onSubmit={async value => { setError(''); try { await update.mutateAsync(value); void navigate(listPath) } catch (cause) { setError(errorMessage(cause)) } }} /></DataBodyTemplate.Group></DataBodyTemplate>
}
