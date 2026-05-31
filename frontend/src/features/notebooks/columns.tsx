import type { DataGridColumnDef } from '@loykin/gridkit'
import { Button } from '@/components/ui/button'
import StatusBadge from '@/shared/components/StatusBadge'
import { notebookProxyURL } from './api'
import type { NotebookServer, NotebookVolume } from './api'

// ── Notebook server columns (state-dependent: busy) ────────────────────────

export function getNotebookColumns(
  busy: string | null,
  onStop: (name: string) => void,
  onStart: (name: string) => void,
  onDelete: (name: string) => void,
): DataGridColumnDef<NotebookServer>[] {
  return [
    {
      accessorKey: 'name',
      header: 'Name',
      meta: { minWidth: 160 },
      cell: ({ row }) => <span className="font-medium">{row.original.name}</span>,
    },
    {
      accessorKey: 'status',
      header: 'Status',
      meta: { minWidth: 110 },
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      accessorKey: 'env',
      header: 'Environment',
      meta: { minWidth: 200, flex: 1 },
      cell: ({ row }) => (
        <span className="font-mono text-xs text-muted-foreground">{row.original.env || '—'}</span>
      ),
    },
    {
      accessorKey: 'work_dir',
      header: 'Work Dir',
      meta: { minWidth: 180 },
      cell: ({ row }) => (
        <span className="font-mono text-xs text-muted-foreground">{row.original.work_dir || '—'}</span>
      ),
    },
    {
      id: 'volume',
      header: 'Volume',
      meta: { minWidth: 120 },
      cell: ({ row }) => (
        <span className="font-mono text-xs text-muted-foreground">
          {row.original.volume_id ? row.original.volume_id.slice(0, 8) + '…' : '—'}
        </span>
      ),
    },
    {
      id: 'actions',
      header: '',
      meta: { minWidth: 200, align: 'right' },
      cell: ({ row }) => {
        const { name, status } = row.original
        const isBusy = busy === name
        const isStarting = status === 'provisioning' || status === 'starting'
        const isStopping = status === 'stopping'
        return (
          <div className="flex justify-end gap-1">
            {(isStarting || isStopping) && (
              <span className="text-xs text-muted-foreground animate-pulse px-2 py-1">
                {isStarting ? 'Starting…' : 'Stopping…'}
              </span>
            )}
            {status === 'running' && (
              <>
                <a
                  href={notebookProxyURL(name)}
                  target="_blank"
                  rel="noreferrer"
                  onClick={e => e.stopPropagation()}
                  className="rounded px-2 py-1 text-xs text-primary hover:bg-primary/10"
                >
                  Open
                </a>
                <Button variant="ghost" size="xs" disabled={isBusy}
                  onClick={e => { e.stopPropagation(); onStop(name) }}>
                  {isBusy ? '…' : 'Stop'}
                </Button>
              </>
            )}
            {(status === 'stopped' || status === 'failed') && (
              <Button variant="ghost" size="xs" disabled={isBusy}
                onClick={e => { e.stopPropagation(); onStart(name) }}>
                {isBusy ? '…' : 'Start'}
              </Button>
            )}
            <Button variant="ghost" size="xs" disabled={isBusy}
              onClick={e => { e.stopPropagation(); onDelete(name) }}
              className="text-muted-foreground hover:text-foreground">
              Delete
            </Button>
          </div>
        )
      },
    },
  ]
}

// ── Notebook volume columns (state-dependent: busy) ────────────────────────

export function getNotebookVolumeColumns(
  busy: string | null,
  workerIdMap: Record<string, string>,
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
        const raw = row.original.worker_id
        if (!raw) return <span className="text-xs text-muted-foreground">—</span>
        const display = workerIdMap[raw] ?? raw
        return <span className="text-xs text-muted-foreground" title={raw}>{display}</span>
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
      meta: { minWidth: 160, align: 'right' },
      cell: ({ row }) => {
        const vol = row.original
        const isBusy = busy === vol.id
        return (
          <div className="flex justify-end gap-1">
            {vol.status === 'released' && (
              <Button variant="ghost" size="xs" disabled={isBusy}
                onClick={e => { e.stopPropagation(); onAttach(vol.id) }}>
                Attach
              </Button>
            )}
            <Button variant="ghost" size="xs" disabled={isBusy || vol.status === 'bound'}
              title={vol.status === 'bound' ? 'Delete the notebook server first' : undefined}
              onClick={e => { e.stopPropagation(); onPurge(vol) }}
              className="text-destructive hover:bg-destructive/10 hover:text-destructive disabled:opacity-30">
              {isBusy ? '…' : 'Purge'}
            </Button>
          </div>
        )
      },
    },
  ]
}
