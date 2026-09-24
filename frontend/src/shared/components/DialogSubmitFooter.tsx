import type { ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { DialogFooter } from '@/components/ui/dialog'
import { actionLabel, type SubmitVerb } from '@/lib/copy'

/**
 * Footer of a dialog that submits something (upload, test, submit template):
 * Cancel, then the primary action labelled like FormSubmitBar —
 * "Upload Object" / "Uploading…". Show the dialog's error in its body.
 */
export function DialogSubmitFooter({ verb, noun, icon, pending, disabled, onCancel, onSubmit }: {
  verb: SubmitVerb
  noun: string
  icon?: ReactNode
  pending: boolean
  /** Extra reasons to block submit; `pending` always blocks it. */
  disabled?: boolean
  onCancel: () => void
  onSubmit: () => void
}) {
  return (
    <DialogFooter>
      <Button variant="outline" onClick={onCancel} disabled={pending}>Cancel</Button>
      <Button onClick={onSubmit} disabled={pending || disabled}>
        {icon}
        {actionLabel(verb, noun, pending)}
      </Button>
    </DialogFooter>
  )
}
