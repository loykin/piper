import { useEffect, useState } from 'react'
import { useNavigate } from '@/lib/router'
import { useProjectId } from '@/lib/projectContext'
import { DataBodyTemplate, PageTopBar } from '@loykin/designkit'
import { ScheduleForm } from '@/features/schedules/components/ScheduleForm'

export default function WorkflowCreatePage() {
  const navigate = useNavigate()
  const projectId = useProjectId()
  const [draftYaml, setDraftYaml] = useState<string | undefined>(undefined)

  useEffect(() => {
    const draft = sessionStorage.getItem('piper.pipeline.editor.draft')
    if (draft) setDraftYaml(draft)
  }, [])

  return (
    <DataBodyTemplate
      topBar={<PageTopBar left="Schedules / Create Schedule" />}
      title="Create Schedule"
      description="Register a pipeline and choose how it should be triggered."
    >
      <DataBodyTemplate.Group layout="stacked" title="Schedule" description="Pipeline source and trigger settings.">
        <ScheduleForm
          initialYaml={draftYaml}
          onCreated={(scheduleId) => navigate(`/projects/${projectId}/schedules/${scheduleId}`)}
          onCancel={() => navigate(`/projects/${projectId}/schedules`)}
        />
      </DataBodyTemplate.Group>
    </DataBodyTemplate>
  )
}
