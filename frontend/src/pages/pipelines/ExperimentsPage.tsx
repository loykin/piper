import { useMemo } from 'react'
import { SidePanelProvider, useSidePanel } from '@loykin/side-panel'
import { DataGrid, type DataGridColumnDef } from '@loykin/gridkit'
import { DataBodyTemplate } from '@loykin/designkit'
import { useRuns } from '@/features/runs/hooks'
import { ExperimentDetailPanel } from '@/features/runs/components/ExperimentDetailPanel'
import type { Run } from '@/features/runs/types'

interface ExperimentRow {
  name: string
  runs: number
  success: number
  failed: number
  running: number
  latest: string
}

function ExperimentsPageInner() {
  const { open } = useSidePanel()
  const { data: runs = [], isLoading } = useRuns()

  const experiments = useMemo<ExperimentRow[]>(() => {
    const map = new Map<string, Run[]>()
    for (const r of runs) {
      if (!r.experiment) continue
      const list = map.get(r.experiment) ?? []
      list.push(r)
      map.set(r.experiment, list)
    }
    return Array.from(map.entries())
      .map(([name, list]) => ({
        name,
        runs: list.length,
        success: list.filter(r => r.status === 'success').length,
        failed: list.filter(r => r.status === 'failed').length,
        running: list.filter(r => r.status === 'running').length,
        latest: list.sort((a, b) => b.started_at.localeCompare(a.started_at))[0]?.started_at ?? '',
      }))
      .sort((a, b) => b.latest.localeCompare(a.latest))
  }, [runs])

  const columns = useMemo<DataGridColumnDef<ExperimentRow>[]>(() => [
    { id: 'name',    header: 'Experiment',  accessorKey: 'name',    meta: { minWidth: 220 } },
    { id: 'runs',    header: 'Runs',        accessorKey: 'runs',    meta: { minWidth: 80 } },
    { id: 'success', header: 'Success',     accessorKey: 'success', meta: { minWidth: 80 },
      cell: ({ row }) => <span className="text-green-400">{row.original.success}</span> },
    { id: 'failed',  header: 'Failed',      accessorKey: 'failed',  meta: { minWidth: 80 },
      cell: ({ row }) => row.original.failed > 0
        ? <span className="text-red-400">{row.original.failed}</span>
        : <span>{row.original.failed}</span> },
    { id: 'running', header: 'Running',     accessorKey: 'running', meta: { minWidth: 80 },
      cell: ({ row }) => row.original.running > 0
        ? <span className="text-blue-400">{row.original.running}</span>
        : <span>{row.original.running}</span> },
    { id: 'latest',  header: 'Latest Run',  accessorKey: 'latest',
      cell: ({ row }) => (
        <span className="text-muted-foreground text-xs">
          {new Date(row.original.latest).toLocaleString()}
        </span>
      ) },
  ], [])

  return (
    <DataBodyTemplate
      title="Experiments"
      description="Grouped sweep runs. Click an experiment to compare runs by params and metrics."
    >
      <DataBodyTemplate.Body>
        <DataGrid
          data={experiments}
          columns={columns}
          isLoading={isLoading}
          emptyMessage="No experiments yet. Submit a sweep via POST /runs/sweep."
          tableWidthMode="fill-last"
          rowHeight={44}
          rowCursor
          onRowClick={(row) => open(<ExperimentDetailPanel experiment={row.name} />, { size: 800 })}
        />
      </DataBodyTemplate.Body>
    </DataBodyTemplate>
  )
}

export default function ExperimentsPage() {
  return (
    <SidePanelProvider defaultSize={800} defaultMinSize={580} defaultMaxSize={1200}>
      <ExperimentsPageInner />
    </SidePanelProvider>
  )
}
