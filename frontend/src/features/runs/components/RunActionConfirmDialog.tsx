import { ConfirmDialog } from '@/shared/components/ConfirmDialog'

export type RunConfirmVerb = 'cancel'

interface RunActionConfirmDialogProps {
  runId: string
  action: RunConfirmVerb | null
  /** Called when the dialog closes without confirming (Back, Esc, outside click). */
  onDismiss: () => void
  cancelling: boolean
  onConfirmCancel: () => void
}

// Cancel confirmation for a run — identical AlertDialog copy and behavior
// between the full RunDetailPage and the RunDetailPanel side-panel view.
export function RunActionConfirmDialog({
  runId,
  action,
  onDismiss,
  cancelling,
  onConfirmCancel,
}: RunActionConfirmDialogProps) {
  return (
    <ConfirmDialog
      open={action != null}
      onCancel={onDismiss}
      title="Cancel this run?"
      description={`Run ${runId} will be stopped immediately.`}
      cancelLabel="Back"
      pending={cancelling}
      confirmLabel={cancelling ? 'Cancelling…' : 'Cancel run'}
      onConfirm={() => {
        onConfirmCancel()
        onDismiss()
      }}
    />
  )
}
