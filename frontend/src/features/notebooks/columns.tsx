import type { DataGridColumnDef } from '@loykin/gridkit'
import { ExternalLink, HardDriveDownload, Play, Square, Trash2 } from 'lucide-react'
import { IconButton } from '@/components/ui/icon-button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import StatusBadge from '@/shared/components/StatusBadge'
import { notebookProxyURL } from './api'
import type { NotebookServer, NotebookVolume } from './api'

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
          <div className="flex justify-end items-center gap-0.5">
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
          </div>
        )
      },
    },
  ]
}

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
      accessorKey: 'worker_id',
      header: 'Node',
      meta: { minWidth: 140 },
      cell: ({ row }) => {
        const workerID = row.original.worker_id
        return <span className="text-xs text-muted-foreground">{workerID || '—'}</span>
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
          <div className="flex justify-end items-center gap-0.5">
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
          </div>
        )
      },
    },
  ]
}
