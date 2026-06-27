import { useMemo } from 'react'
import { type DataGridColumnDef } from '@loykin/gridkit'
import { CalendarClock, CopyPlus, Play, Trash2 } from 'lucide-react'
import { IconButton } from '@/components/ui/icon-button'
import { Badge } from '@/components/ui/badge'
import type { PipelineTemplate } from './types'

function relativeTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime()
  const min = Math.floor(diff / 60000)
  if (min < 1) return 'just now'
  if (min < 60) return `${min}m ago`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr}h ago`
  return `${Math.floor(hr / 24)}d ago`
}

interface PipelineColumnCallbacks {
  onRun: (t: PipelineTemplate) => void
  onDeploy: (t: PipelineTemplate) => void
  onNewVersion: (t: PipelineTemplate) => void
  onDelete: (t: PipelineTemplate) => void
}

export function usePipelineColumns(callbacks: PipelineColumnCallbacks): DataGridColumnDef<PipelineTemplate>[] {
  return useMemo<DataGridColumnDef<PipelineTemplate>[]>(() => [
    {
      // grouping key — hidden via visibilityState
      accessorKey: 'template_id',
      header: 'Template',
    },
    {
      id: 'version',
      header: 'Version',
      size: 80,
      cell: ({ row }) => (
        <span className="font-medium text-foreground">v{row.original.version}</span>
      ),
    },
    {
      id: 'description',
      header: 'Description',
      meta: { flex: 2, minWidth: 120 },
      cell: ({ row }) => (
        <span className="truncate text-sm text-muted-foreground">
          {row.original.description || '—'}
        </span>
      ),
    },
    {
      id: 'tags',
      header: 'Tags',
      meta: { flex: 1, minWidth: 80 },
      cell: ({ row }) => {
        const tags = row.original.tags ?? []
        if (tags.length === 0) return <span className="text-sm text-muted-foreground">—</span>
        return (
          <div className="flex flex-wrap gap-1">
            {tags.map(tag => (
              <Badge key={tag} variant="secondary" className="text-xs">{tag}</Badge>
            ))}
          </div>
        )
      },
    },
    {
      id: 'created_at',
      header: 'Submitted',
      size: 100,
      cell: ({ row }) => (
        <span className="text-sm text-muted-foreground" title={row.original.created_at}>
          {relativeTime(row.original.created_at)}
        </span>
      ),
    },
    {
      id: 'actions',
      header: '',
      size: 116,
      cell: ({ row }) => (
        <div className="flex items-center justify-end gap-0.5">
          <IconButton icon={<Play />} label="Run" onClick={() => callbacks.onRun(row.original)} />
          <IconButton
            icon={<CalendarClock />}
            label="Deploy to schedule"
            onClick={() => callbacks.onDeploy(row.original)}
          />
          <IconButton
            icon={<CopyPlus />}
            label={`New version from v${row.original.version}`}
            onClick={() => callbacks.onNewVersion(row.original)}
          />
          <IconButton
            icon={<Trash2 />}
            label="Delete"
            onClick={() => callbacks.onDelete(row.original)}
            className="text-destructive hover:bg-destructive/10"
          />
        </div>
      ),
    },
  ], [callbacks])
}
