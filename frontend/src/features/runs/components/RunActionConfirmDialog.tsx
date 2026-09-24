import { ConfirmDialog } from '@/shared/components/ConfirmDialog'
import type { useConfirmAction } from '@/shared/hooks/useConfirmAction'

export type RunConfirmVerb = 'cancel'

interface RunActionConfirmDialogProps {
  runId: string
  confirmation: ReturnType<typeof useConfirmAction<RunConfirmVerb>>
  cancelling: boolean
  onConfirmCancel: () => Promise<unknown>
}

// Cancel confirmation for a run — identical copy and behavior between the
// full RunDetailPage and the RunDetailPanel side-panel view. Stays open with
// the error if the cancel fails.
export function RunActionConfirmDialog({ runId, confirmation, cancelling, onConfirmCancel }: RunActionConfirmDialogProps) {
  return (
    <ConfirmDialog
      open={confirmation.open}
      onCancel={confirmation.cancel}
      verb="Cancel"
      noun="run"
      description={`Run ${runId} will be stopped immediately.`}
      error={confirmation.error}
      pending={cancelling}
      onConfirm={() => void confirmation.confirm(async () => { await onConfirmCancel() })}
    />
  )
}
