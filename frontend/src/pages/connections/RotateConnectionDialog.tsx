import { useState } from 'react'
import { KeyRound, Plus, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import type { Connection } from '@/features/connections/types'
import { useRotateConnection } from '@/features/connections/hooks'

type DataEntry = { key: string; value: string }
const emptyEntry = (): DataEntry => ({ key: '', value: '' })

export default function RotateConnectionDialog({
  target,
  onClose,
}: {
  target: Connection | null
  onClose: () => void
}) {
  const rotateConnection = useRotateConnection()
  const [entries, setEntries] = useState<DataEntry[]>([emptyEntry()])
  const [error, setError] = useState('')

  function updateEntry(idx: number, field: keyof DataEntry, value: string) {
    setEntries(prev => prev.map((e, i) => i === idx ? { ...e, [field]: value } : e))
  }

  function addEntry() {
    setEntries(prev => [...prev, emptyEntry()])
  }

  function removeEntry(idx: number) {
    setEntries(prev => {
      const next = prev.filter((_, i) => i !== idx)
      return next.length ? next : [emptyEntry()]
    })
  }

  function handleOpenChange(open: boolean) {
    if (!open) { setEntries([emptyEntry()]); setError(''); onClose() }
  }

  async function submit() {
    if (!target) return
    setError('')
    const data = Object.fromEntries(
      entries.filter(e => e.key.trim() && e.value.trim()).map(e => [e.key.trim(), e.value])
    )
    try {
      await rotateConnection.mutateAsync({ name: target.name, data })
      setEntries([emptyEntry()])
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  const canSubmit = entries.some(e => e.key.trim() && e.value.trim())

  return (
    <Dialog open={!!target} onOpenChange={handleOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <KeyRound className="size-4" />
            Rotate {target?.name}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className="grid grid-cols-[1fr_1fr_auto] gap-x-2 pb-1">
            <Label className="text-xs text-muted-foreground">Key</Label>
            <Label className="text-xs text-muted-foreground">Value</Label>
            <span />
          </div>
          {entries.map((entry, idx) => (
            <div key={idx} className="grid grid-cols-[1fr_1fr_auto] gap-x-2 items-center">
              <Input
                value={entry.key}
                onChange={e => updateEntry(idx, 'key', e.target.value)}
                placeholder="token"
                className="font-mono text-sm"
              />
              <Input
                type="password"
                value={entry.value}
                onChange={e => updateEntry(idx, 'value', e.target.value)}
                placeholder="••••••••"
                className="font-mono text-sm"
              />
              <IconButton
                icon={<Trash2 />}
                label="Remove"
                onClick={() => removeEntry(idx)}
                className="text-muted-foreground hover:text-destructive"
              />
            </div>
          ))}
          <Button variant="ghost" size="sm" onClick={addEntry} className="text-muted-foreground">
            <Plus className="mr-1.5 size-3.5" />
            Add field
          </Button>
          {error && <p className="text-sm text-destructive">{error}</p>}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={rotateConnection.isPending}>Cancel</Button>
          <Button onClick={() => void submit()} disabled={!canSubmit || rotateConnection.isPending}>
            {rotateConnection.isPending ? 'Rotating...' : 'Rotate'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
