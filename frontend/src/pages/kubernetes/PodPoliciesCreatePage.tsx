import { useNavigate } from 'react-router-dom'
import { DataBodyTemplate, PageTopBar } from '@loykin/designkit'
import { PodPolicyForm } from '@/features/workers/components/PodPolicyForm'

export default function PodPoliciesCreatePage() {
  const navigate = useNavigate()

  return (
    <DataBodyTemplate
      topBar={<PageTopBar left="Kubernetes / Pod Policies / New" />}
      title="New Pod Policy"
      description="Configure a per-worker pod template baseline. Applied at notebook dispatch time, before the manifest's own pod_template settings."
    >
      <DataBodyTemplate.Body>
        <PodPolicyForm
          onSaved={() => navigate('/kubernetes/pod-policies')}
          onCancel={() => navigate('/kubernetes/pod-policies')}
        />
      </DataBodyTemplate.Body>
    </DataBodyTemplate>
  )
}
