import { useState } from 'react'
import type { Row } from '@tanstack/react-table'
import { useNavigate, useSearchParams } from '@/lib/router'
import { useProjectId } from '@/lib/projectContext'
import { Plus } from 'lucide-react'
import { DataBodyTemplate } from '@loykin/designkit'
import { DataGrid } from '@loykin/gridkit'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { Button } from '@/components/ui/button'
import { usePipelines, useDeletePipeline, useRunPipeline } from '@/features/pipelines/hooks'
import { usePipelineColumns } from '@/features/pipelines/columns'
import { DeployModal } from '@/features/pipelines/components/DeployModal'
import { PipelineDetailPanel } from '@/features/pipelines/components/PipelineDetailPanel'
import type { PipelineTemplate } from '@/features/pipelines/types'

function GroupHeader({ row }: { row: Row<PipelineTemplate> }) {
  const first = row.subRows[0]?.original
  const name = first?.name ?? '—'
  const description = first?.description
  const count = row.subRows.length
  return (
    <span className="flex items-baseline gap-2">
      <span className="font-medium text-foreground">{name}</span>
      {description && <span className="text-xs text-muted-foreground">{description}</span>}
      <span className="text-xs font-normal text-muted-foreground">{count} version{count !== 1 ? 's' : ''}</span>
    </span>
  )
}

function PipelinesListPageInner() {
  const navigate = useNavigate()
  const projectId = useProjectId()
  const { open } = useSidePanel()
  const [searchParams] = useSearchParams()
  const filterName = searchParams.get('name') ?? ''

  const { data: templates = [], isLoading, error: loadError } = usePipelines(filterName || undefined)
  const { mutateAsync: deletePipeline } = useDeletePipeline()
  const { mutateAsync: runPipeline } = useRunPipeline()

  const [deployTarget, setDeployTarget] = useState<PipelineTemplate | null>(null)
  const [deployCron, setDeployCron] = useState('0 2 * * *')
  const [deployEnabled, setDeployEnabled] = useState(true)
  const [actionError, setActionError] = useState('')

  async function handleRun(t: PipelineTemplate) {
    setActionError('')
    try {
      const result = await runPipeline({ id: t.id })
      navigate(`/projects/${projectId}/runs/${result.id}`)
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }

  async function handleDelete(t: PipelineTemplate) {
    if (!confirm(`Delete "${t.name}" v${t.version} (${t.id.slice(0, 8)}…)? This also deletes the snapshot for this version.`)) return
    setActionError('')
    try {
      await deletePipeline(t.id)
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    }
  }

  function openDeploy(t: PipelineTemplate) {
    setDeployTarget(t)
    setDeployCron('0 2 * * *')
    setDeployEnabled(true)
    setActionError('')
  }

  function openNewVersionFrom(t: PipelineTemplate) {
    const params = new URLSearchParams({
      template_id: t.template_id,
      from_version: t.id,
      name: t.name,
      source: t.volume_id ? 'notebook-volume' : 'local',
    })
    if (t.volume_id) params.set('volume', t.volume_id)
    else params.set('root', '.')
    navigate(`/projects/${projectId}/pipelines/editor?${params.toString()}`)
  }

  function openDetail(t: PipelineTemplate) {
    open(
      <PipelineDetailPanel
        template={t}
        onRun={(x) => void handleRun(x)}
        onDeploy={openDeploy}
        onNewVersion={openNewVersionFrom}
        onDelete={(x) => void handleDelete(x)}
      />,
      { size: 520 },
    )
  }

  const columns = usePipelineColumns({
    onRun: (t) => void handleRun(t),
    onDeploy: openDeploy,
    onNewVersion: openNewVersionFrom,
    onDelete: (t) => void handleDelete(t),
  })

  return (
    <>
      <DataBodyTemplate
        title="Pipeline Templates"
        description="Each submit creates a new versioned snapshot. Deploy to schedule or run on demand."
        actions={
          <Button size="sm" onClick={() => navigate(`/projects/${projectId}/pipelines/editor`)}>
            <Plus size={14} className="mr-1.5" /> New Template
          </Button>
        }
      >
        <DataBodyTemplate.Body>
          {loadError && (
            <p className="mb-4 text-sm text-destructive">Failed to load pipeline templates.</p>
          )}
          {actionError && (
            <p className="mb-4 text-sm text-destructive">{actionError}</p>
          )}
          <DataGrid
            data={templates}
            columns={columns}
            isLoading={isLoading}
            enableGrouping
            grouping={['template_id']}
            visibilityState={{ template_id: false }}
            renderGroupRow={(row) => <GroupHeader row={row} />}
            emptyMessage="No pipeline templates yet."
            tableWidthMode="fill-last"
            rowHeight={44}
            rowCursor
            onRowClick={(row) => openDetail(row)}
          />
        </DataBodyTemplate.Body>
      </DataBodyTemplate>

      <DeployModal
        template={deployTarget}
        cron={deployCron}
        enabled={deployEnabled}
        onCronChange={setDeployCron}
        onEnabledChange={setDeployEnabled}
        onClose={() => setDeployTarget(null)}
        onDeployed={(scheduleId) => navigate(`/projects/${projectId}/schedules/${scheduleId}`)}
        error={actionError}
      />
    </>
  )
}

export default function PipelinesListPage() {
  return (
    <SidePanelProvider defaultSize={520} defaultMinSize={380} defaultMaxSize={900}>
      <PipelinesListPageInner />
    </SidePanelProvider>
  )
}
