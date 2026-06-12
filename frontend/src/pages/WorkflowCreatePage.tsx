import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useProjectId } from '@/lib/projectContext'
import { DataPage } from '@loykin/designkit'
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
    <DataPage>
      <DataPage.Header>
        <DataPage.TitleBlock
          title="Create Schedule"
          description="Register a pipeline and choose how it should be triggered."
        />
      </DataPage.Header>

      <DataPage.Content>
        <DataPage.Group surface="bordered" className="max-w-2xl">
          <ScheduleForm
            initialYaml={draftYaml}
            onCreated={(scheduleId) => navigate(`/projects/${projectId}/schedules/${scheduleId}`)}
            onCancel={() => navigate(`/projects/${projectId}/schedules`)}
          />
        </DataPage.Group>
      </DataPage.Content>
    </DataPage>
  )
}
