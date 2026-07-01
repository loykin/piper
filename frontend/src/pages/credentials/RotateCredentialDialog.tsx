import { useState } from 'react'
import { KeyRound, Plus, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import type { Credential } from '@/features/credentials/types'
import type { UseMutationResult } from '@tanstack/react-query'

type DataEntry = { key: string; value: string }
const emptyEntry = (): DataEntry => ({ key: '', value: '' })

function initialEntries(target: Credential | null): DataEntry[] {
  if (target?.kind === 'git') return [{ key: 'username', value: '' }, { key: 'token', value: '' }]
  return [emptyEntry()]
}

export default function RotateCredentialDialog({
  target,
  rotateCredential,
  onClose,
}: {
  target: Credential | null
  rotateCredential: UseMutationResult<void, Error, { name: string; data: Record<string, string> }>
  onClose: () => void
}) {
  const [entries, setEntries] = useState<DataEntry[]>(initialEntries(target))
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
    if (!open) { setEntries(initialEntries(null)); setError(''); onClose() }
  }

  async function submit() {
    if (!target) return
    setError('')
    const data = Object.fromEntries(
      entries.filter(e => e.key.trim() && (target.kind === 'generic' || e.value.trim())).map(e => [e.key.trim(), e.value])
    )
    try {
      await rotateCredential.mutateAsync({ name: target.name, data })
      setEntries(initialEntries(null))
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  const canSubmit = target?.kind === 'git'
    ? entries.some(e => ['token', 'password'].includes(e.key.trim()) && e.value.trim())
    : entries.some(e => e.key.trim())

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
            <div key={idx} className="grid grid-cols-[1fr_1fr_auto] items-center gap-x-2">
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
                placeholder="secret value"
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
          <Button variant="outline" onClick={onClose} disabled={rotateCredential.isPending}>Cancel</Button>
          <Button onClick={() => void submit()} disabled={!canSubmit || rotateCredential.isPending}>
            {rotateCredential.isPending ? 'Rotating...' : 'Rotate'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
