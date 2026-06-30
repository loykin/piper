import { KeyRound, Plus, Trash2 } from 'lucide-react'
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { useSecrets } from '@/features/secrets/hooks'
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
  const { data: secrets = [] } = useSecrets()
  const activeSecrets = secrets.filter(secret => !secret.disabled)

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
          const selectedSecret = activeSecrets.find(secret => secret.name === item.secretName)
          const secretKeys = selectedSecret?.keys ?? []
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
                    <SelectItem value="secret">Secret</SelectItem>
                  </SelectContent>
                </Select>
                <IconButton
                  icon={<Trash2 />}
                  label="Remove"
                  onClick={() => onRemove(rowIndex)}
                  className="text-destructive hover:bg-destructive/10"
                />
              </div>
              {item.source === 'secret' ? (
                <div className="grid grid-cols-2 gap-2">
                  <Select
                    value={item.secretName}
                    onValueChange={value => onUpdate(rowIndex, { secretName: value ?? '', secretKey: '' })}
                  >
                    <SelectTrigger size="sm">
                      <SelectValue placeholder="Select secret" />
                    </SelectTrigger>
                    <SelectContent>
                      {activeSecrets.length === 0 ? (
                        <SelectItem value="__none__" disabled>No active secrets</SelectItem>
                      ) : activeSecrets.map(secret => (
                        <SelectItem key={secret.name} value={secret.name}>
                          <span className="inline-flex items-center gap-1.5">
                            <KeyRound size={12} />
                            {secret.name}
                          </span>
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <Select
                    value={item.secretKey}
                    onValueChange={value => onUpdate(rowIndex, { secretKey: value ?? '' })}
                    disabled={!item.secretName || secretKeys.length === 0}
                  >
                    <SelectTrigger size="sm">
                      <SelectValue placeholder="Select key" />
                    </SelectTrigger>
                    <SelectContent>
                      {secretKeys.length === 0 ? (
                        <SelectItem value="__none__" disabled>No keys</SelectItem>
                      ) : secretKeys.map(key => (
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
