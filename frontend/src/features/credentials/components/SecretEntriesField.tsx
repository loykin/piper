import { Plus, Trash2 } from 'lucide-react'
import { Label } from '@loykin/designkit'
import { useFieldArray, type Control, type UseFormRegister } from 'react-hook-form'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import type { SecretEntriesValues } from '../secretEntries'

/**
 * Key/value rows for a credential's write-only secret data. Values are
 * password inputs; the server never returns stored values.
 */
export function SecretEntriesField<T extends SecretEntriesValues>({
  control, register, label, keyPlaceholder, error,
}: {
  control: Control<T>
  register: UseFormRegister<T>
  label?: string
  keyPlaceholder?: string
  error?: string
}) {
  const { fields, append, remove } = useFieldArray({
    control: control as unknown as Control<SecretEntriesValues>,
    name: 'entries',
  })
  const reg = register as unknown as UseFormRegister<SecretEntriesValues>
  return (
    <div className="space-y-1.5">
      {label && <Label className="text-xs">{label}</Label>}
      <div className="grid grid-cols-[1fr_1fr_auto] gap-x-2 pb-1">
        <span className="text-xs text-muted-foreground">Key</span>
        <span className="text-xs text-muted-foreground">Value</span>
        <span />
      </div>
      {fields.map((field, idx) => (
        <div key={field.id} className="grid grid-cols-[1fr_1fr_auto] items-center gap-x-2">
          <Input
            aria-label={`Key ${idx + 1}`}
            placeholder={keyPlaceholder}
            className="h-8 font-mono text-sm"
            {...reg(`entries.${idx}.key`)}
          />
          <Input
            aria-label={`Value ${idx + 1}`}
            type="password"
            autoComplete="new-password"
            placeholder="secret value"
            className="h-8 font-mono text-sm"
            {...reg(`entries.${idx}.value`)}
          />
          <IconButton
            icon={<Trash2 />}
            label="Remove"
            disabled={fields.length <= 1}
            onClick={() => remove(idx)}
            className="text-muted-foreground hover:text-destructive"
          />
        </div>
      ))}
      <Button type="button" variant="ghost" size="sm" className="text-muted-foreground" onClick={() => append({ key: '', value: '' })}>
        <Plus className="mr-1.5 size-3.5" />
        Add Field
      </Button>
      {error && <p className="text-xs text-destructive">{error}</p>}
    </div>
  )
}
