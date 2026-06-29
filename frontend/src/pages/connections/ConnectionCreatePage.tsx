import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { DataBodyTemplate, PageTopBar } from '@loykin/designkit'
import { Plus, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useProjectId } from '@/lib/projectContext'
import { useCreateConnection } from '@/features/connections/hooks'
import type { ConnectionType } from '@/features/connections/types'

type DataEntry = { key: string; value: string }
const emptyEntry = (): DataEntry => ({ key: '', value: '' })

const GIT_FIELDS: DataEntry[] = [
  { key: 'token', value: '' },
  { key: 'username', value: '' },
]

const REGISTRY_FIELDS: DataEntry[] = [
  { key: 'username', value: '' },
  { key: 'password', value: '' },
]

export default function ConnectionCreatePage() {
  const projectId = useProjectId()
  const navigate = useNavigate()
  const createConnection = useCreateConnection()

  const [name, setName] = useState('')
  const [type, setType] = useState<ConnectionType>('git')
  const [endpoint, setEndpoint] = useState('')
  const [entries, setEntries] = useState<DataEntry[]>(GIT_FIELDS)
  const [error, setError] = useState('')

  function handleTypeChange(next: ConnectionType) {
    setType(next)
    setEntries(next === 'git' ? GIT_FIELDS : REGISTRY_FIELDS)
  }

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
    const data = Object.fromEntries(
      entries.filter(e => e.key.trim() && e.value.trim()).map(e => [e.key.trim(), e.value])
    )
    try {
      await createConnection.mutateAsync({ name: name.trim(), type, endpoint: endpoint.trim(), data })
      void navigate({ to: `/projects/${projectId}/connections` })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  const canSubmit = name.trim() && entries.some(e => e.key.trim() && e.value.trim())

  const endpointPlaceholder = type === 'git'
    ? 'https://github.com/myorg/ (optional — leave blank for any repo)'
    : 'registry.example.com (optional)'

  return (
    <DataBodyTemplate
      topBar={<PageTopBar left="Connections" />}
      title="New Connection"
      description="Infrastructure credential for accessing a git repository or container registry."
      actions={
        <div className="flex gap-2">
          <Button variant="outline" onClick={() => void navigate({ to: `/projects/${projectId}/connections` })}>
            Cancel
          </Button>
          <Button onClick={() => void submit()} disabled={!canSubmit || createConnection.isPending}>
            {createConnection.isPending ? 'Creating...' : 'Create Connection'}
          </Button>
        </div>
      }
    >
      <DataBodyTemplate.Body>
        <div className="max-w-xl space-y-6">
          <div className="space-y-1.5">
            <Label htmlFor="conn-name">Name</Label>
            <Input
              id="conn-name"
              value={name}
              onChange={e => setName(e.target.value)}
              placeholder="github-myorg"
              className="font-mono"
            />
          </div>

          <div className="space-y-1.5">
            <Label>Type</Label>
            <div className="flex gap-2">
              {(['git', 'registry'] as ConnectionType[]).map(t => (
                <Button
                  key={t}
                  variant={type === t ? 'default' : 'outline'}
                  size="sm"
                  onClick={() => handleTypeChange(t)}
                >
                  {t}
                </Button>
              ))}
            </div>
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="conn-endpoint">
              {type === 'git' ? 'Endpoint URL prefix' : 'Registry host'}
              <span className="ml-1 text-xs text-muted-foreground">(optional)</span>
            </Label>
            <Input
              id="conn-endpoint"
              value={endpoint}
              onChange={e => setEndpoint(e.target.value)}
              placeholder={endpointPlaceholder}
              className="font-mono text-sm"
            />
            {type === 'git' && (
              <p className="text-xs text-muted-foreground">
                If set, this credential is only used for repos under this prefix. Must end with <code>/</code>.
              </p>
            )}
          </div>

          <div className="space-y-2">
            <Label>Credentials</Label>
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
              Add field
            </Button>
          </div>

          {error && <p className="text-sm text-destructive">{error}</p>}
        </div>
      </DataBodyTemplate.Body>
    </DataBodyTemplate>
  )
}
