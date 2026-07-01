// runs feature — Step list component
import { RefreshCw } from 'lucide-react'
import { DataGrid, DataGridPaginationCompact, type DataGridColumnDef } from '@loykin/gridkit'
import { PanelTemplate } from '@loykin/designkit'
import { IconButton } from '@/components/ui/icon-button'
import StatusBadge from '@/shared/components/StatusBadge'
import { RowActions } from '@/shared/components/RowActions'
import type { Step } from '../types'

function formatStepTime(value?: string): string {
  if (!value) return '—'
  return new Date(value).toLocaleTimeString()
}

function formatStepDuration(step: Step): string {
  if (!step.started_at) return '—'
  const start = new Date(step.started_at).getTime()
  const end = step.ended_at ? new Date(step.ended_at).getTime() : Date.now()
  const ms = Math.max(0, end - start)
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  return `${(ms / 60000).toFixed(1)}m`
}

interface StepListProps {
  steps: Step[]
  selectedId: string | null
  onSelect: (id: string) => void
  onRetry?: (stepName: string) => void
}

export function StepList({ steps, selectedId, onSelect, onRetry }: StepListProps) {
  const columns: DataGridColumnDef<Step>[] = [
    {
      accessorKey: 'step_name',
      header: 'Step',
      meta: { minWidth: 220, flex: 1 },
      cell: ({ row }) => (
        <button
          type="button"
          onClick={() => onSelect(row.original.step_name)}
          className={`text-left ${selectedId === row.original.step_name ? 'text-indigo-300' : 'text-gray-200 hover:text-white'}`}
        >
          {row.original.step_name}
        </button>
      ),
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
      cell: ({ row }) => <span className="text-gray-400">{formatStepTime(row.original.started_at)}</span>,
    },
    {
      id: 'duration',
      header: 'Duration',
      meta: { minWidth: 120, align: 'right' },
      cell: ({ row }) => <span className="text-gray-400">{formatStepDuration(row.original)}</span>,
    },
    {
      id: 'error',
      header: 'Error',
      meta: { minWidth: 240, flex: 1 },
      cell: ({ row }) => (
        <span className="block truncate text-xs text-red-400">{row.original.error ?? '—'}</span>
      ),
    },
    ...(onRetry ? [{
      id: 'actions',
      header: '',
      meta: { minWidth: 90, align: 'right' as const },
      cell: ({ row }: { row: { original: Step } }) => (
        <RowActions>
          <IconButton icon={<RefreshCw />} label="Retry"
            disabled={row.original.status !== 'failed'}
            onClick={(e) => {
              e.stopPropagation()
              onRetry(row.original.step_name)
            }}
            className="text-yellow-400 hover:bg-yellow-950" />
        </RowActions>
      ),
    }] : []),
  ]

  return (
    <PanelTemplate title="Steps" className="mb-4 h-auto" bodyClassName="p-0 [&_.dg-shell]:h-full [&_.dg-table-wrapper]:min-h-0 [&_.dg-table-wrapper]:flex-1">
      <DataGrid
        data={steps}
        columns={columns}
        tableWidthMode="fill-last"
        rowHeight={44}
        rowCursor
        onRowClick={(row) => onSelect(row.step_name)}
        pagination={{ pageSize: 10 }}
        footer={(table) => (
          <div className="flex h-9 items-center justify-between px-1 text-xs text-muted-foreground">
            <span>{steps.length} results</span>
            <DataGridPaginationCompact table={table} />
          </div>
        )}
      />
    </PanelTemplate>
  )
}
