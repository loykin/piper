import { Copy, HardDriveDownload, Trash2, X } from 'lucide-react'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import StatusBadge from '@/shared/components/StatusBadge'
import type { NotebookVolume } from '@/features/notebooks/types'

interface NotebookVolumeDetailPanelProps {
  volume: NotebookVolume
  busy: boolean
  onAttach: (volId: string) => void
  onPurge: (volume: NotebookVolume) => void
}

export function NotebookVolumeDetailPanel({ volume, busy, onAttach, onPurge }: NotebookVolumeDetailPanelProps) {
  const { close } = useSidePanel()

  function handleCopyId() {
    void navigator.clipboard.writeText(volume.id)
  }

  return (
    <PanelTemplate
      eyebrow="Notebook volume"
      title={volume.label}
      status={<StatusBadge status={volume.status} />}
      actions={
        <div className="flex items-center gap-1">
          {volume.status === 'released' && (
            <IconButton
              icon={<HardDriveDownload />}
              label="Attach"
              disabled={busy}
              onClick={() => { onAttach(volume.id); void close() }}
            />
          )}
          <IconButton
            icon={<Trash2 />}
            label={volume.status === 'bound' ? 'Delete the notebook server first' : 'Purge'}
            disabled={busy || volume.status === 'bound'}
            onClick={() => { onPurge(volume); void close() }}
            className="text-destructive hover:bg-destructive/10"
          />
          <Button variant="ghost" size="icon-sm" onClick={() => void close()}>
            <X />
            <span className="sr-only">Close</span>
          </Button>
        </div>
      }
    >
      <PanelTemplate.Section title="Details">
        <dl className="space-y-2">
          <PanelTemplate.Row label="ID">
            <div className="flex items-start gap-2">
              <span className="break-all font-mono text-xs">{volume.id}</span>
              <IconButton icon={<Copy />} label="Copy ID" onClick={handleCopyId} />
            </div>
          </PanelTemplate.Row>
          <PanelTemplate.Row label="Work Dir">
            <span className="break-all font-mono text-xs text-muted-foreground">{volume.work_dir || '—'}</span>
          </PanelTemplate.Row>
          <PanelTemplate.Row label="Runtime">{volume.runtime_id || '—'}</PanelTemplate.Row>
          <PanelTemplate.Row label="Created">{new Date(volume.created_at).toLocaleString()}</PanelTemplate.Row>
          <PanelTemplate.Row label="Updated">{new Date(volume.updated_at).toLocaleString()}</PanelTemplate.Row>
        </dl>
      </PanelTemplate.Section>
    </PanelTemplate>
  )
}
