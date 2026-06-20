import type { DataGridColumnDef } from '@loykin/gridkit'
import StatusBadge from '@/shared/components/StatusBadge'
import type { Run, Step } from './api'

// ── Helpers ───────────────────────────────────────────────────────────────────

function elapsed(startedAt: string, endedAt?: string): string {
  const ms = (endedAt ? new Date(endedAt) : new Date()).getTime() - new Date(startedAt).getTime()
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  return `${(ms / 60000).toFixed(1)}m`
}

const STEP_COLOR: Record<string, string> = {
  done:     'bg-green-500',
  success:  'bg-green-500',
  running:  'bg-blue-400 animate-pulse',
  failed:   'bg-red-500',
  skipped:  'bg-yellow-600',
  canceled: 'bg-orange-500',
  pending:  'bg-gray-600',
}

function StepDots({ steps }: { steps: Step[] }) {
  if (!steps.length) return <span className="text-xs text-muted-foreground">—</span>
  return (
    <div className="flex flex-wrap items-center gap-1">
      {steps.map((s) => (
        <span
          key={s.step_name}
          title={`${s.step_name}: ${s.status}`}
          className={`inline-block h-3.5 w-3.5 rounded-sm ${STEP_COLOR[s.status] ?? 'bg-gray-600'}`}
        />
      ))}
    </div>
  )
}

// ── Run columns (no state dependency) ────────────────────────────────────────

export const runColumns: DataGridColumnDef<Run>[] = [
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
  {
    accessorKey: 'pipeline_name',
    header: 'Pipeline',
    meta: { minWidth: 140, flex: 1 },
    cell: ({ row }) => (
      <span className="block truncate text-sm" title={row.original.pipeline_name}>
        {row.original.pipeline_name}
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
    accessorKey: 'started_at',
    header: 'Started',
    meta: { minWidth: 180 },
    cell: ({ row }) => <span className="text-xs text-muted-foreground">{new Date(row.original.started_at).toLocaleString()}</span>,
  },
  {
    id: 'duration',
    header: 'Duration',
    meta: { minWidth: 100 },
    cell: ({ row }) => <span className="text-xs text-muted-foreground">{elapsed(row.original.started_at, row.original.ended_at)}</span>,
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
      <span className="text-gray-400">
        {row.original.started_at ? new Date(row.original.started_at).toLocaleTimeString() : '—'}
      </span>
    ),
  },
  {
    id: 'duration',
    header: 'Duration',
    meta: { minWidth: 120, align: 'right' },
    cell: ({ row }) => {
      const step = row.original
      if (!step.started_at) return <span className="text-gray-400">—</span>
      const start = new Date(step.started_at).getTime()
      const end = step.ended_at ? new Date(step.ended_at).getTime() : Date.now()
      const ms = Math.max(0, end - start)
      let dur: string
      if (ms < 1000) dur = `${ms}ms`
      else if (ms < 60000) dur = `${(ms / 1000).toFixed(1)}s`
      else dur = `${(ms / 60000).toFixed(1)}m`
      return <span className="text-gray-400">{dur}</span>
    },
  },
  {
    id: 'error',
    header: 'Error',
    meta: { minWidth: 240, flex: 1 },
    cell: ({ row }) => (
      <span className="block truncate text-xs text-red-400">{row.original.error ?? '—'}</span>
    ),
  },
]
