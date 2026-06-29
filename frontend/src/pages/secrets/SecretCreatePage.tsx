import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { DataBodyTemplate, PageTopBar } from '@loykin/designkit'
import { Plus, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useProjectId } from '@/lib/projectContext'
import { useCreateSecret } from '@/features/secrets/hooks'

type DataEntry = { key: string; value: string }

const emptyEntry = (): DataEntry => ({ key: '', value: '' })

export default function SecretCreatePage() {
  const projectId = useProjectId()
  const navigate = useNavigate()
  const createSecret = useCreateSecret()

  const [name, setName] = useState('')
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

  async function submit() {
    setError('')
    const data = Object.fromEntries(entries.filter(e => e.key.trim()).map(e => [e.key.trim(), e.value]))
    try {
      await createSecret.mutateAsync({ name: name.trim(), provider: 'piper-managed', data })
      void navigate({ to: `/projects/${projectId}/secrets` })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  const canSubmit = name.trim() && entries.some(e => e.key.trim())

  return (
    <DataBodyTemplate
      topBar={<PageTopBar left="Secrets" />}
      title="New Secret"
      description="Named key-value credential. Reference any key from pipeline, notebook, or serving specs."
      actions={
        <div className="flex gap-2">
          <Button variant="outline" onClick={() => void navigate({ to: `/projects/${projectId}/secrets` })}>
            Cancel
          </Button>
          <Button onClick={() => void submit()} disabled={!canSubmit || createSecret.isPending}>
            {createSecret.isPending ? 'Creating...' : 'Create Secret'}
          </Button>
        </div>
      }
    >
      <DataBodyTemplate.Body>
        <div className="max-w-xl space-y-6">
          <div className="space-y-1.5">
            <Label htmlFor="secret-name">Name</Label>
            <Input
              id="secret-name"
              value={name}
              onChange={e => setName(e.target.value)}
              placeholder="github-creds"
              className="font-mono"
            />
          </div>

          <div className="space-y-2">
            <Label>Data</Label>
            <div className="grid grid-cols-[1fr_1fr_auto] gap-x-2 pb-1">
              <span className="text-xs text-muted-foreground">Key</span>
              <span className="text-xs text-muted-foreground">Value</span>
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
              Add entry
            </Button>
          </div>

          {error && <p className="text-sm text-destructive">{error}</p>}
        </div>
      </DataBodyTemplate.Body>
    </DataBodyTemplate>
  )
}
