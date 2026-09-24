import { Copy, HardDriveDownload, Trash2 } from 'lucide-react'
import { PanelTemplate } from '@loykin/designkit'
import { IconButton } from '@/components/ui/icon-button'
import StatusBadge from '@/shared/components/StatusBadge'
import { PanelCloseButton, PanelPlaceholder } from '@/shared/components/PanelPlaceholder'
import { useNotebookVolumes } from '@/features/notebooks/hooks'
import type { NotebookVolume } from '@/features/notebooks/types'
import { fmtDate } from '@/lib/format'

interface NotebookVolumeDetailPanelProps {
  id: string
  onAttach: (volId: string) => void
  onPurge: (volume: NotebookVolume) => void
}

// There is no single-volume endpoint; the (polled) project volume list is
// small and also keeps the status badge live.
export function NotebookVolumeDetailPanel({ id, onAttach, onPurge }: NotebookVolumeDetailPanelProps) {
  const query = useNotebookVolumes()
  const volumes = query.data
  const volume = volumes?.find(v => v.id === id)
  if (!volume) return <PanelPlaceholder query={query} noun="volume" />

  return (
    <PanelTemplate
      eyebrow="Notebook Volume"
      title={volume.label}
      status={<StatusBadge status={volume.status} />}
      actions={
        <div className="flex items-center gap-1">
          {volume.status === 'released' && (
            <IconButton
              icon={<HardDriveDownload />}
              label="Attach"
              onClick={() => onAttach(volume.id)}
            />
          )}
          <IconButton
            icon={<Trash2 />}
            label={volume.status === 'bound' ? 'Delete the notebook server first' : 'Purge'}
            disabled={volume.status === 'bound'}
            onClick={() => onPurge(volume)}
            className="text-destructive hover:bg-destructive/10"
          />
          <PanelCloseButton />
        </div>
      }
    >
      <PanelTemplate.Section title="Details">
        <dl className="space-y-2">
          <PanelTemplate.Row label="ID">
            <div className="flex items-start gap-2">
              <span className="break-all font-mono text-xs">{volume.id}</span>
              <IconButton icon={<Copy />} label="Copy ID" onClick={() => void navigator.clipboard.writeText(volume.id)} />
            </div>
          </PanelTemplate.Row>
          <PanelTemplate.Row label="Work Dir">
            <span className="break-all font-mono text-xs text-muted-foreground">{volume.work_dir || '—'}</span>
          </PanelTemplate.Row>
          <PanelTemplate.Row label="Runtime">{volume.runtime_id || '—'}</PanelTemplate.Row>
          <PanelTemplate.Row label="Created">{fmtDate(volume.created_at)}</PanelTemplate.Row>
          <PanelTemplate.Row label="Updated">{fmtDate(volume.updated_at)}</PanelTemplate.Row>
        </dl>
      </PanelTemplate.Section>
    </PanelTemplate>
  )
}
