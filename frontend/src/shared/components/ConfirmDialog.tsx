import type { ReactNode } from 'react'
import { actionLabel, type ConfirmVerb } from '@/lib/copy'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'

/**
 * Confirmation for a destructive action. The copy is derived from `verb` and
 * `noun` so every dialog reads the same way: title "Delete this schedule?"
 * (a question, so sentence case), button "Delete Schedule" / "Deleting…". Name the specific target in
 * `description`.
 *
 * Back `open`/`onCancel` with useDeleteTarget or useConfirmAction; closing
 * (Cancel, Esc, outside click) only ever calls `onCancel`, so the dialog
 * can't reopen itself against a different target after the action completes.
 */
export function ConfirmDialog({
  open, onCancel, verb, noun, description, error, pending, onConfirm,
}: {
  open: boolean
  onCancel: () => void
  verb: ConfirmVerb
  /** Lower-case resource noun, e.g. "schedule", "alert rule". */
  noun: string
  description: ReactNode
  error?: string
  pending?: boolean
  onConfirm: () => void
}) {
  return (
    <AlertDialog open={open} onOpenChange={next => { if (!next) onCancel() }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{verb} this {noun}?</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        {error && <p className="text-sm text-destructive">{error}</p>}
        <AlertDialogFooter>
          {/* "Cancel" would be ambiguous next to "Cancel run". */}
          <AlertDialogCancel>{verb === 'Cancel' ? 'Back' : 'Cancel'}</AlertDialogCancel>
          <AlertDialogAction variant="destructive" disabled={pending} onClick={onConfirm}>
            {actionLabel(verb, noun, pending ?? false)}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
