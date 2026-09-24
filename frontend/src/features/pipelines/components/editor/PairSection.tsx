import { Plus, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import type { PipelineKeyValueDraft } from '../../editor'

interface PairSectionProps {
  label: string
  emptyText: string
  keyPlaceholder?: string
  valuePlaceholder?: string
  items: PipelineKeyValueDraft[]
  onAdd: () => void
  onRemove: (rowIndex: number) => void
  onUpdate: (rowIndex: number, patch: Partial<PipelineKeyValueDraft>) => void
}

export function PairSection({ label, emptyText, keyPlaceholder = 'key', valuePlaceholder = 'value', items, onAdd, onRemove, onUpdate }: PairSectionProps) {
  return (
    <div>
      <div className="mb-2 flex items-center justify-between">
        <label className="block text-[11px] uppercase tracking-wider text-muted-foreground">{label}</label>
        <Button variant="outline" size="sm" onClick={onAdd}><Plus size={14} className="mr-1.5" /> Add</Button>
      </div>
      <div className="space-y-2">
        {items.length === 0 ? (
          <p className="text-xs text-muted-foreground">{emptyText}</p>
        ) : items.map((item, i) => (
          <div key={i} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] gap-2">
            <Input value={item.key} placeholder={keyPlaceholder} onChange={e => onUpdate(i, { key: e.target.value })} />
            <Input value={item.value} placeholder={valuePlaceholder} onChange={e => onUpdate(i, { value: e.target.value })} />
            <IconButton icon={<Trash2 />} label="Remove" onClick={() => onRemove(i)} className="text-destructive hover:bg-destructive/10" />
          </div>
        ))}
      </div>
    </div>
  )
}
