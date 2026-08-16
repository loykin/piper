import { Copy, Download, Trash2, X } from 'lucide-react'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { storageObjectURL, type StorageObjectInfo } from '@/features/storage/api'
import { fmtBytes, fmtDate } from '@/features/storage/format'

interface ObjectDetailPanelProps {
  projectId: string
  object: StorageObjectInfo
  onDelete: (object: StorageObjectInfo) => void
}

export function ObjectDetailPanel({ projectId, object, onDelete }: ObjectDetailPanelProps) {
  const { close } = useSidePanel()

  function handleDownload() {
    window.open(storageObjectURL(projectId, object.key), '_blank', 'noopener,noreferrer')
  }

  function handleCopyKey() {
    void navigator.clipboard.writeText(object.key)
  }

  return (
    <PanelTemplate
      eyebrow="Uploaded object"
      title={object.key}
      actions={
        <div className="flex items-center gap-1">
          <IconButton icon={<Download />} label="Download" onClick={handleDownload} />
          <IconButton
            icon={<Trash2 />}
            label="Delete"
            className="text-destructive hover:bg-destructive/10"
            onClick={() => { onDelete(object); void close() }}
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
          <PanelTemplate.Row label="Key">
            <div className="flex items-start gap-2">
              <span className="break-all font-mono text-xs">{object.key}</span>
              <IconButton icon={<Copy />} label="Copy key" onClick={handleCopyKey} />
            </div>
          </PanelTemplate.Row>
          <PanelTemplate.Row label="Size">{fmtBytes(object.size)}</PanelTemplate.Row>
          <PanelTemplate.Row label="Modified">{fmtDate(object.modified_at)}</PanelTemplate.Row>
        </dl>
      </PanelTemplate.Section>
    </PanelTemplate>
  )
}
