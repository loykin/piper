import type { DataGridColumnDef } from '@loykin/gridkit'
import StatusBadge from '@/shared/components/StatusBadge'
import type { Run, Step } from './api'
import type { ExperimentSummary } from './types'
import { statusTone, toneFill, toneText } from '@/shared/status'
import { fmtDate, fmtTime } from '@/lib/format'

// ── Helpers ───────────────────────────────────────────────────────────────────

function elapsed(startedAt: string, endedAt?: string): string {
  const ms = (endedAt ? new Date(endedAt) : new Date()).getTime() - new Date(startedAt).getTime()
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  return `${(ms / 60000).toFixed(1)}m`
}

function StepDots({ steps }: { steps: Step[] }) {
  if (!steps.length) return <span className="text-xs text-muted-foreground">—</span>
  return (
    <div className="flex flex-wrap items-center gap-1">
      {steps.map((s) => (
        <span
          key={s.step_name}
          title={`${s.step_name}: ${s.status}`}
          className={`inline-block h-3.5 w-3.5 rounded-sm ${toneFill[statusTone(s.status)]}${s.status === 'running' ? ' animate-pulse' : ''}`}
        />
      ))}
    </div>
  )
}

// ── Run columns (no state dependency) ────────────────────────────────────────

export const runColumns: DataGridColumnDef<Run>[] = [
  {
    accessorKey: 'started_at',
    header: 'Started',
    meta: { minWidth: 160 },
    cell: ({ row }) => (
      <span className="text-xs text-muted-foreground">
        {fmtDate(row.original.started_at)}
      </span>
    ),
  },
  {
    accessorKey: 'pipeline_name',
    header: 'Pipeline',
    meta: { minWidth: 140, flex: 1 },
    cell: ({ row }) => (
      <span className="flex items-baseline gap-1.5 truncate" title={row.original.pipeline_name}>
        <span className="truncate text-sm">{row.original.pipeline_name}</span>
        {row.original.pipeline_version != null && row.original.pipeline_version > 0 && (
          <span className="shrink-0 text-xs text-muted-foreground">v{row.original.pipeline_version}</span>
        )}
      </span>
    ),
  },
  {
    accessorKey: 'status',
    header: 'Status',
    meta: { minWidth: 120 },
    cell: ({ row }) => <StatusBadge status={row.original.status} />,
  },
  {
    id: 'steps',
    header: 'Steps',
    meta: { minWidth: 160 },
    cell: ({ row }) => <StepDots steps={row.original.steps ?? []} />,
  },
  {
    id: 'duration',
    header: 'Duration',
    meta: { minWidth: 100 },
    cell: ({ row }) => (
      <span className="text-xs text-muted-foreground">
        {elapsed(row.original.started_at, row.original.ended_at)}
      </span>
    ),
  },
  {
    accessorKey: 'id',
    header: 'Run ID',
    meta: { minWidth: 200 },
    cell: ({ row }) => (
      <span className="block truncate font-mono text-xs text-primary" title={row.original.id}>
        {row.original.id}
      </span>
    ),
  },
]

// ── Step columns (for RunDetailPage) ─────────────────────────────────────────

export const stepColumns: DataGridColumnDef<Step>[] = [
  {
    accessorKey: 'step_name',
    header: 'Step',
    meta: { minWidth: 220, flex: 1 },
  },
  {
    accessorKey: 'status',
    header: 'Status',
    meta: { minWidth: 140 },
    cell: ({ row }) => <StatusBadge status={row.original.status} />,
  },
  {
    accessorKey: 'started_at',
    header: 'Started',
    meta: { minWidth: 140 },
    cell: ({ row }) => (
      <span className="text-muted-foreground">
        {row.original.started_at ? fmtTime(row.original.started_at) : '—'}
      </span>
    ),
  },
  {
    id: 'duration',
    header: 'Duration',
    meta: { minWidth: 120, align: 'right' },
    cell: ({ row }) => {
      const step = row.original
      if (!step.started_at) return <span className="text-muted-foreground">—</span>
      const start = new Date(step.started_at).getTime()
      const end = step.ended_at ? new Date(step.ended_at).getTime() : Date.now()
      const ms = Math.max(0, end - start)
      let dur: string
      if (ms < 1000) dur = `${ms}ms`
      else if (ms < 60000) dur = `${(ms / 1000).toFixed(1)}s`
      else dur = `${(ms / 60000).toFixed(1)}m`
      return <span className="text-muted-foreground">{dur}</span>
    },
  },
  {
    id: 'error',
    header: 'Error',
    meta: { minWidth: 240, flex: 1 },
    cell: ({ row }) => (
      <span className={`block truncate text-xs ${toneText.danger}`}>{row.original.error ?? '—'}</span>
    ),
  },
]

// ── Experiment columns ────────────────────────────────────────────────────────

export const experimentColumns: DataGridColumnDef<ExperimentSummary>[] = [
  { id: 'name',    header: 'Experiment',  accessorKey: 'name',    meta: { minWidth: 220 } },
  { id: 'runs',    header: 'Runs',        accessorKey: 'runs',    meta: { minWidth: 80 } },
  { id: 'success', header: 'Success',     accessorKey: 'success', meta: { minWidth: 80 },
    cell: ({ row }) => <span className={toneText.success}>{row.original.success}</span> },
  { id: 'failed',  header: 'Failed',      accessorKey: 'failed',  meta: { minWidth: 80 },
    cell: ({ row }) => row.original.failed > 0
      ? <span className={toneText.danger}>{row.original.failed}</span>
      : <span>{row.original.failed}</span> },
  { id: 'running', header: 'Running',     accessorKey: 'running', meta: { minWidth: 80 },
    cell: ({ row }) => row.original.running > 0
      ? <span className={toneText.info}>{row.original.running}</span>
      : <span>{row.original.running}</span> },
  { id: 'latest',  header: 'Latest Run',  accessorKey: 'latest',
    cell: ({ row }) => (
      <span className="text-muted-foreground text-xs">
        {fmtDate(row.original.latest)}
      </span>
    ) },
]
