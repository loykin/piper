import type { RefObject } from 'react'
import { FolderOpen, Plus, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import type { PipelineArtifactDraft } from '../../editor'
import { FileBrowseDropdown } from './BrowseDropdowns'

interface ArtifactSectionProps {
  label: string
  kind: 'inputs' | 'outputs'
  items: PipelineArtifactDraft[]
  canBrowse: boolean
  volumeFiles: string[]
  activeBrowseKey: string | null
  browseQuery: string
  browseRef: RefObject<HTMLDivElement | null>
  onAdd: () => void
  onRemove: (rowIndex: number) => void
  onUpdate: (rowIndex: number, patch: Partial<PipelineArtifactDraft>) => void
  onBrowseToggle: (key: string) => void
  onBrowseQueryChange: (q: string) => void
  onBrowseSelect: (rowIndex: number, file: string) => void
}

export function ArtifactSection({
  label, kind, items, canBrowse, volumeFiles, activeBrowseKey, browseQuery, browseRef,
  onAdd, onRemove, onUpdate, onBrowseToggle, onBrowseQueryChange, onBrowseSelect,
}: ArtifactSectionProps) {
  return (
    <div>
      <div className="mb-2 flex items-center justify-between">
        <label className="block text-[11px] uppercase tracking-wider text-muted-foreground">{label}</label>
        <Button variant="outline" size="sm" onClick={onAdd}><Plus size={14} className="mr-1.5" /> Add</Button>
      </div>
      <div className="space-y-2">
        {items.length === 0 ? (
          <p className="text-xs text-muted-foreground">No {label.toLowerCase()}.</p>
        ) : items.map((item, rowIndex) => {
          const browseKey = `${kind}-${rowIndex}`
          const isBrowseOpen = activeBrowseKey === browseKey
          return (
            <div key={rowIndex} className="grid gap-1">
              <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2">
                <Input value={item.name} placeholder="name" onChange={e => onUpdate(rowIndex, { name: e.target.value })} />
                <IconButton icon={<Trash2 />} label="Remove" onClick={() => onRemove(rowIndex)} className="text-destructive hover:bg-destructive/10" />
              </div>
              <div ref={isBrowseOpen ? browseRef : null} className="relative">
                <div className="flex gap-1.5">
                  <Input value={item.path} placeholder="path in workspace" onChange={e => onUpdate(rowIndex, { path: e.target.value })} />
                  {canBrowse && (
                    <IconButton
                      icon={<FolderOpen />}
                      label="Browse Volume Files"
                      onClick={() => onBrowseToggle(browseKey)}
                    />
                  )}
                </div>
                {isBrowseOpen && (
                  <FileBrowseDropdown
                    files={volumeFiles}
                    query={browseQuery}
                    onQueryChange={onBrowseQueryChange}
                    onSelect={f => onBrowseSelect(rowIndex, f)}
                  />
                )}
              </div>
              <Input value={item.from} placeholder="from (task name)" onChange={e => onUpdate(rowIndex, { from: e.target.value })} />
            </div>
          )
        })}
      </div>
    </div>
  )
}
