import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { DataBodyTemplate, PageTopBar, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@loykin/designkit'
import { Plus, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useProjectId } from '@/lib/projectContext'
import { useCreateCredential } from '@/features/credentials/hooks'
import type { CredentialKind } from '@/features/credentials/types'

type DataEntry = { key: string; value: string }
const emptyEntry = (): DataEntry => ({ key: '', value: '' })

const GENERIC_FIELDS: DataEntry[] = [emptyEntry()]
const GIT_FIELDS: DataEntry[] = [
  { key: 'username', value: '' },
  { key: 'token', value: '' },
]

export default function CredentialCreatePage() {
  const projectId = useProjectId()
  const navigate = useNavigate()
  const createCredential = useCreateCredential()

  const [name, setName] = useState('')
  const [kind, setKind] = useState<CredentialKind>('generic')
  const [endpoint, setEndpoint] = useState('')
  const [entries, setEntries] = useState<DataEntry[]>(GENERIC_FIELDS)
  const [error, setError] = useState('')

  function handleKindChange(next: CredentialKind) {
    setKind(next)
    setEndpoint('')
    setEntries(next === 'git' ? GIT_FIELDS : GENERIC_FIELDS)
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
      entries.filter(e => e.key.trim() && (kind === 'generic' || e.value.trim())).map(e => [e.key.trim(), e.value])
    )
    try {
      await createCredential.mutateAsync({
        name: name.trim(),
        kind,
        endpoint: kind === 'git' ? endpoint.trim() : undefined,
        data,
      })
      void navigate({ to: `/projects/${projectId}/credentials` })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  const canSubmit = name.trim() && entries.some(e => e.key.trim() && (kind === 'generic' || e.value.trim()))

  return (
    <DataBodyTemplate
      topBar={<PageTopBar left="Credentials" />}
      title="New Credential"
      description="Create a write-only credential. Stored values are never returned by the API."
      actions={
        <div className="flex gap-2">
          <Button variant="outline" onClick={() => void navigate({ to: `/projects/${projectId}/credentials` })}>
            Cancel
          </Button>
          <Button onClick={() => void submit()} disabled={!canSubmit || createCredential.isPending}>
            {createCredential.isPending ? 'Creating...' : 'Create Credential'}
          </Button>
        </div>
      }
    >
      <DataBodyTemplate.Body>
        <div className="max-w-xl space-y-6">
          <div className="space-y-1.5">
            <Label htmlFor="credential-name">Name</Label>
            <Input
              id="credential-name"
              value={name}
              onChange={e => setName(e.target.value)}
              placeholder={kind === 'git' ? 'github-acme' : 'wandb'}
              className="font-mono"
            />
          </div>

          <div className="space-y-1.5">
            <Label>Kind</Label>
            <Select value={kind} onValueChange={value => handleKindChange((value ?? 'generic') as CredentialKind)}>
              <SelectTrigger className="w-44">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="generic">Generic</SelectItem>
                <SelectItem value="git">Git</SelectItem>
              </SelectContent>
            </Select>
          </div>

          {kind === 'git' && (
            <div className="space-y-1.5">
              <Label htmlFor="credential-endpoint">
                Endpoint URL prefix
                <span className="ml-1 text-xs text-muted-foreground">(optional)</span>
              </Label>
              <Input
                id="credential-endpoint"
                value={endpoint}
                onChange={e => setEndpoint(e.target.value)}
                placeholder="https://github.com/myorg/"
                className="font-mono text-sm"
              />
            </div>
          )}

          <div className="space-y-2">
            <Label>{kind === 'generic' ? 'Data' : 'Credentials'}</Label>
            <div className="grid grid-cols-[1fr_1fr_auto] gap-x-2 pb-1">
              <span className="text-xs text-muted-foreground">Key</span>
              <span className="text-xs text-muted-foreground">Value</span>
              <span />
            </div>
            {entries.map((entry, idx) => (
              <div key={idx} className="grid grid-cols-[1fr_1fr_auto] items-center gap-x-2">
                <Input
                  value={entry.key}
                  onChange={e => updateEntry(idx, 'key', e.target.value)}
                  placeholder={kind === 'git' ? 'token' : 'api_key'}
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
          </div>

          {error && <p className="text-sm text-destructive">{error}</p>}
        </div>
      </DataBodyTemplate.Body>
    </DataBodyTemplate>
  )
}
