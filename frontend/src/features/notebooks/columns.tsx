import type { DataGridColumnDef } from '@loykin/gridkit'
import { ExternalLink, HardDriveDownload, Play, Square, Trash2 } from 'lucide-react'
import { IconButton } from '@/components/ui/icon-button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import StatusBadge from '@/shared/components/StatusBadge'
import { RowActions } from '@/shared/components/RowActions'
import { notebookProxyURL } from './api'
import type { NotebookServer, NotebookVolume, NotebookHistory } from './api'

// ── Notebook server columns (state-dependent: busy) ────────────────────────

export function getNotebookColumns(
  busy: string | null,
  onStop: (name: string) => void,
  onStart: (name: string) => void,
  onDelete: (name: string) => void,
  projectId: string,
): DataGridColumnDef<NotebookServer>[] {
  return [
    {
      accessorKey: 'name',
      header: 'Name',
      meta: { minWidth: 160 },
      cell: ({ row }) => (
        <span className="font-medium text-foreground">{row.original.name}</span>
      ),
    },
    {
      accessorKey: 'status',
      header: 'Status',
      meta: { minWidth: 110 },
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      id: 'actions',
      header: '',
      meta: { minWidth: 160, align: 'right' },
      cell: ({ row }) => {
        const { name, status } = row.original
        const isBusy = busy === name
        const isStarting = status === 'provisioning' || status === 'starting'
        const isStopping = status === 'stopping'
        return (
          <RowActions>
            {(isStarting || isStopping) && (
              <span className="text-xs text-muted-foreground animate-pulse px-2">
                {isStarting ? 'Starting…' : 'Stopping…'}
              </span>
            )}
            {status === 'running' && (
              <>
                <Tooltip>
                  <TooltipTrigger>
                    <a
                      href={notebookProxyURL(projectId, name)}
                      target="_blank"
                      rel="noreferrer"
                      onClick={e => e.stopPropagation()}
                      className="inline-flex size-7 items-center justify-center rounded-[min(var(--radius-md),12px)] text-primary hover:bg-muted"
                    >
                      <ExternalLink size={16} />
                    </a>
                  </TooltipTrigger>
                  <TooltipContent>Open</TooltipContent>
                </Tooltip>
                <IconButton icon={<Square />} label="Stop" disabled={isBusy}
                  onClick={e => { e.stopPropagation(); onStop(name) }} />
              </>
            )}
            {(status === 'stopped' || status === 'failed') && (
              <IconButton icon={<Play />} label="Start" disabled={isBusy}
                onClick={e => { e.stopPropagation(); onStart(name) }} />
            )}
            <IconButton icon={<Trash2 />} label="Delete" disabled={isBusy}
              onClick={e => { e.stopPropagation(); onDelete(name) }}
              className="text-muted-foreground hover:text-destructive" />
          </RowActions>
        )
      },
    },
  ]
}

// ── Notebook history columns ────────────────────────────────────────────────

function elapsed(deployedAt: string, stoppedAt: string): string {
  const ms = new Date(stoppedAt).getTime() - new Date(deployedAt).getTime()
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  if (ms < 3_600_000) return `${(ms / 60000).toFixed(1)}m`
  return `${(ms / 3_600_000).toFixed(1)}h`
}

export const notebookHistoryColumns: DataGridColumnDef<NotebookHistory>[] = [
  {
    accessorKey: 'name',
    header: 'Notebook',
    meta: { minWidth: 140 },
    cell: ({ row }) => (
      <span className="block truncate font-medium">{row.original.name}</span>
    ),
  },
  {
    accessorKey: 'status',
    header: 'Final Status',
    meta: { minWidth: 120 },
    cell: ({ row }) => <StatusBadge status={row.original.status} />,
  },
  {
    accessorKey: 'image',
    header: 'Image',
    meta: { minWidth: 140, flex: 1 },
    cell: ({ row }) => (
      <span className="block truncate font-mono text-xs text-muted-foreground" title={row.original.image || undefined}>
        {row.original.image || '—'}
      </span>
    ),
  },
  {
    id: 'runtime_id',
    header: 'Runtime',
    meta: { minWidth: 140 },
    cell: ({ row }) => (
      <span className="text-xs text-muted-foreground">{row.original.runtime_id || '—'}</span>
    ),
  },
  {
    id: 'deployed_at',
    header: 'Started',
    meta: { minWidth: 150 },
    cell: ({ row }) => (
      <span className="text-xs text-muted-foreground">
        {new Date(row.original.deployed_at).toLocaleString()}
      </span>
    ),
  },
  {
    id: 'stopped_at',
    header: 'Ended',
    meta: { minWidth: 150 },
    cell: ({ row }) => (
      <span className="text-xs text-muted-foreground">
        {new Date(row.original.stopped_at).toLocaleString()}
      </span>
    ),
  },
  {
    id: 'duration',
    header: 'Duration',
    meta: { minWidth: 90 },
    cell: ({ row }) => (
      <span className="text-xs text-muted-foreground">
        {elapsed(row.original.deployed_at, row.original.stopped_at)}
      </span>
    ),
  },
]

// ── Notebook volume columns (state-dependent: busy) ────────────────────────

export function getNotebookVolumeColumns(
  busy: string | null,
  onAttach: (volId: string) => void,
  onPurge: (vol: NotebookVolume) => void,
): DataGridColumnDef<NotebookVolume>[] {
  return [
    {
      accessorKey: 'label',
      header: 'Label',
      meta: { minWidth: 160 },
      cell: ({ row }) => <span className="font-medium">{row.original.label}</span>,
    },
    {
      accessorKey: 'id',
      header: 'ID',
      meta: { minWidth: 280 },
      cell: ({ row }) => (
        <span className="font-mono text-xs text-muted-foreground">{row.original.id}</span>
      ),
    },
    {
      accessorKey: 'work_dir',
      header: 'Work Dir',
      meta: { minWidth: 240, flex: 1 },
      cell: ({ row }) => (
        <span className="font-mono text-xs text-muted-foreground">{row.original.work_dir || '—'}</span>
      ),
    },
    {
      accessorKey: 'runtime_id',
      header: 'Runtime',
      meta: { minWidth: 140 },
      cell: ({ row }) => {
        const runtimeID = row.original.runtime_id
        return <span className="text-xs text-muted-foreground">{runtimeID || '—'}</span>
      },
    },
    {
      accessorKey: 'status',
      header: 'Status',
      meta: { minWidth: 100 },
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      accessorKey: 'created_at',
      header: 'Created',
      meta: { minWidth: 160 },
      cell: ({ row }) => (
        <span className="text-xs text-muted-foreground">
          {new Date(row.original.created_at).toLocaleString()}
        </span>
      ),
    },
    {
      id: 'actions',
      header: '',
      meta: { minWidth: 120, align: 'right' },
      cell: ({ row }) => {
        const vol = row.original
        const isBusy = busy === vol.id
        return (
          <RowActions>
            {vol.status === 'released' && (
              <IconButton icon={<HardDriveDownload />} label="Attach" disabled={isBusy}
                onClick={e => { e.stopPropagation(); onAttach(vol.id) }} />
            )}
            <IconButton
              icon={<Trash2 />}
              label={vol.status === 'bound' ? 'Delete the notebook server first' : 'Purge'}
              disabled={isBusy || vol.status === 'bound'}
              onClick={e => { e.stopPropagation(); onPurge(vol) }}
              className="text-destructive hover:bg-destructive/10 hover:text-destructive" />
          </RowActions>
        )
      },
    },
  ]
}
