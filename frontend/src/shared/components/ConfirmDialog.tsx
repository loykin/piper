import type { ReactNode } from 'react'
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
 * Confirmation for a destructive or irreversible action. Back `open`/`onCancel`
 * with useDeleteTarget or useConfirmAction; closing (Cancel, Esc, outside
 * click) only ever calls `onCancel`, so the dialog can't reopen itself
 * against a different target after the action completes.
 */
export function ConfirmDialog({
  open, onCancel, title, description, error, cancelLabel = 'Cancel', confirmLabel, pending, onConfirm, destructive = true,
}: {
  open: boolean
  onCancel: () => void
  title: ReactNode
  description: ReactNode
  error?: string
  cancelLabel?: ReactNode
  confirmLabel: ReactNode
  pending?: boolean
  onConfirm: () => void
  destructive?: boolean
}) {
  return (
    <AlertDialog open={open} onOpenChange={next => { if (!next) onCancel() }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        {error && <p className="text-sm text-destructive">{error}</p>}
        <AlertDialogFooter>
          <AlertDialogCancel>{cancelLabel}</AlertDialogCancel>
          <AlertDialogAction variant={destructive ? 'destructive' : 'default'} disabled={pending} onClick={onConfirm}>
            {confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
