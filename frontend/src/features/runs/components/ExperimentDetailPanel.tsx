import { useMemo, useState } from 'react'
import { useQueries } from '@tanstack/react-query'
import { ArrowUpDown, X } from 'lucide-react'
import { DataGrid, type DataGridColumnDef } from '@loykin/gridkit'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { Button } from '@/components/ui/button'
import StatusBadge from '@/shared/components/StatusBadge'
import { useRuns, runKeys } from '@/features/runs/hooks'
import { getRunMetrics } from '@/features/runs/api'
import type { Run, RunMetrics } from '@/features/runs/types'
import { useProjectId } from '@/lib/projectContext'
import { RunDetailPanel } from './RunDetailPanel'

interface SortState { step: string; key: string; order: 'asc' | 'desc' }

function parseParams(json?: string): Record<string, unknown> {
  if (!json) return {}
  try { return JSON.parse(json) as Record<string, unknown> } catch { return {} }
}

export function ExperimentDetailPanel({ experiment }: { experiment: string }) {
  const { close, open } = useSidePanel()
  const projectId = useProjectId()
  const [sort, setSort] = useState<SortState | null>(null)

  const { data: runs = [], isLoading } = useRuns(
    sort
      ? { experiment, metric_step: sort.step, metric_key: sort.key, metric_order: sort.order }
      : { experiment }
  )

  const metricQueries = useQueries({
    queries: runs.map(r => ({
      queryKey: runKeys.metrics(projectId, r.id),
      queryFn: () => getRunMetrics(projectId, r.id),
    })),
  })

  const metricsById = useMemo<Record<string, RunMetrics>>(() => {
    const m: Record<string, RunMetrics> = {}
    runs.forEach((r, i) => { m[r.id] = metricQueries[i]?.data ?? {} })
    return m
  }, [runs, metricQueries])

  const { paramKeys, metricCols } = useMemo(() => {
    const pkeys = new Set<string>()
    const mcols = new Set<string>()
    for (const r of runs) {
      for (const k of Object.keys(parseParams(r.params_json))) pkeys.add(k)
    }
    for (const m of Object.values(metricsById)) {
      for (const [step, vals] of Object.entries(m)) {
        for (const key of Object.keys(vals)) mcols.add(`${step}::${key}`)
      }
    }
    return { paramKeys: Array.from(pkeys).sort(), metricCols: Array.from(mcols).sort() }
  }, [runs, metricsById])

  const columns = useMemo<DataGridColumnDef<Run>[]>(() => {
    const cols: DataGridColumnDef<Run>[] = [
      {
        id: 'status', header: 'Status', meta: { minWidth: 100 },
        cell: ({ row }) => <StatusBadge status={row.original.status} />,
      },
      {
        id: 'started_at', header: 'Started', meta: { minWidth: 130 },
        cell: ({ row }) => (
          <span className="text-xs text-muted-foreground">
            {new Date(row.original.started_at).toLocaleString()}
          </span>
        ),
      },
    ]
    for (const pk of paramKeys) {
      cols.push({
        id: `param_${pk}`,
        header: pk,
        meta: { minWidth: 90 },
        cell: ({ row }) => {
          const v = parseParams(row.original.params_json)[pk]
          return <span className="text-xs font-mono">{v !== undefined ? String(v) : '—'}</span>
        },
      })
    }
    for (const mc of metricCols) {
      const [step, key] = mc.split('::')
      const isSorted = sort?.step === step && sort?.key === key
      cols.push({
        id: `metric_${mc}`,
        header: () => (
          <Button
            variant="ghost"
            size="sm"
            className={`-ml-1 h-7 gap-1 text-xs font-medium ${isSorted ? 'text-primary' : ''}`}
            onClick={() => setSort(prev =>
              prev?.step === step && prev?.key === key
                ? { step, key, order: prev.order === 'desc' ? 'asc' : 'desc' }
                : { step, key, order: 'desc' }
            )}
          >
            {step}/{key}
            <ArrowUpDown className="h-3 w-3" />
            {isSorted && <span className="text-[10px]">{sort!.order}</span>}
          </Button>
        ),
        meta: { minWidth: 110 },
        cell: ({ row }) => {
          const v = metricsById[row.original.id]?.[step]?.[key]
          return <span className="text-xs font-mono">{v !== undefined ? v.toFixed(4) : '—'}</span>
        },
      })
    }
    return cols
  }, [paramKeys, metricCols, metricsById, sort])

  return (
    <PanelTemplate
      eyebrow="Experiment"
      title={experiment}
      actions={
        <Button variant="ghost" size="icon-sm" onClick={() => void close()}>
          <X className="h-3.5 w-3.5" />
        </Button>
      }
      footer={
        <p className="text-xs text-muted-foreground">
          {runs.length} run{runs.length !== 1 ? 's' : ''} · click metric column header to sort
        </p>
      }
    >
      <PanelTemplate.Section>
        <DataGrid
          data={runs}
          columns={columns}
          isLoading={isLoading}
          emptyMessage="No runs in this experiment."
          tableWidthMode="fill-last"
          rowHeight={44}
          rowCursor
          onRowClick={(row) => open(<RunDetailPanel id={row.id} />, { size: 720 })}
        />
      </PanelTemplate.Section>
    </PanelTemplate>
  )
}
