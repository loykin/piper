import { KeyRound, Plus, Trash2 } from 'lucide-react'
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { useCredentials } from '@/features/credentials/hooks'
import type { EnvVarDraft } from '@/shared/env'

interface EnvVarEditorProps {
  label?: string
  emptyText?: string
  items: EnvVarDraft[]
  onAdd: () => void
  onRemove: (rowIndex: number) => void
  onUpdate: (rowIndex: number, patch: Partial<EnvVarDraft>) => void
}

export function EnvVarEditor({
  label = 'Environment',
  emptyText = 'No environment variables.',
  items,
  onAdd,
  onRemove,
  onUpdate,
}: EnvVarEditorProps) {
  const { data: credentials = [] } = useCredentials()
  const activeCredentials = credentials.filter(credential => credential.kind === 'generic' && !credential.disabled)

  return (
    <div>
      <div className="mb-2 flex items-center justify-between">
        <label className="block text-[11px] uppercase tracking-wider text-muted-foreground">{label}</label>
        <Button variant="outline" size="sm" onClick={onAdd}>
          <Plus size={14} className="mr-1.5" /> Add
        </Button>
      </div>
      <div className="space-y-2">
        {items.length === 0 ? (
          <p className="text-xs text-muted-foreground">{emptyText}</p>
        ) : items.map((item, rowIndex) => {
          const selectedCredential = activeCredentials.find(credential => credential.name === item.credentialName)
          const credentialKeys = selectedCredential?.keys ?? []
          return (
            <div key={rowIndex} className="grid gap-2 rounded-md border border-border bg-background p-2">
              <div className="grid grid-cols-[minmax(0,1fr)_8rem_auto] gap-2">
                <Input
                  value={item.name}
                  placeholder="ENV_NAME"
                  onChange={event => onUpdate(rowIndex, { name: event.target.value })}
                />
                <Select
                  value={item.source}
                  onValueChange={value => onUpdate(rowIndex, {
                    source: (value ?? 'value') as EnvVarDraft['source'],
                  })}
                >
                  <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="value">Value</SelectItem>
                    <SelectItem value="credential">Credential</SelectItem>
                  </SelectContent>
                </Select>
                <IconButton
                  icon={<Trash2 />}
                  label="Remove"
                  onClick={() => onRemove(rowIndex)}
                  className="text-destructive hover:bg-destructive/10"
                />
              </div>
              {item.source === 'credential' ? (
                <div className="grid grid-cols-2 gap-2">
                  <Select
                    value={item.credentialName}
                    onValueChange={value => onUpdate(rowIndex, { credentialName: value ?? '', credentialKey: '' })}
                  >
                    <SelectTrigger size="sm">
                      <SelectValue placeholder="Select credential" />
                    </SelectTrigger>
                    <SelectContent>
                      {activeCredentials.length === 0 ? (
                        <SelectItem value="__none__" disabled>No active credentials</SelectItem>
                      ) : activeCredentials.map(credential => (
                        <SelectItem key={credential.name} value={credential.name}>
                          <span className="inline-flex items-center gap-1.5">
                            <KeyRound size={12} />
                            {credential.name}
                          </span>
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <Select
                    value={item.credentialKey}
                    onValueChange={value => onUpdate(rowIndex, { credentialKey: value ?? '' })}
                    disabled={!item.credentialName || credentialKeys.length === 0}
                  >
                    <SelectTrigger size="sm">
                      <SelectValue placeholder="Select key" />
                    </SelectTrigger>
                    <SelectContent>
                      {credentialKeys.length === 0 ? (
                        <SelectItem value="__none__" disabled>No keys</SelectItem>
                      ) : credentialKeys.map(key => (
                        <SelectItem key={key} value={key}>{key}</SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
              ) : (
                <Input
                  value={item.value}
                  placeholder="value"
                  onChange={event => onUpdate(rowIndex, { value: event.target.value })}
                />
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}
