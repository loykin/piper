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

export type RunConfirmVerb = 'cancel' | 'delete'

interface RunActionConfirmDialogProps {
  runId: string
  action: RunConfirmVerb | null
  onOpenChange: (open: boolean) => void
  cancelling: boolean
  deleting: boolean
  onConfirmCancel: () => void
  onConfirmDelete: () => void
}

// Shared cancel/delete confirmation for a run — identical AlertDialog copy
// and behavior between the full RunDetailPage and the RunDetailPanel
// side-panel view. Only what happens after a successful delete differs
// (navigate away vs. close the panel), so that stays with the caller via
// onConfirmDelete.
export function RunActionConfirmDialog({
  runId,
  action,
  onOpenChange,
  cancelling,
  deleting,
  onConfirmCancel,
  onConfirmDelete,
}: RunActionConfirmDialogProps) {
  return (
    <AlertDialog open={action != null} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {action === 'cancel' ? 'Cancel this run?' : 'Delete this run?'}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {action === 'cancel'
              ? `Run ${runId} will be stopped immediately.`
              : `Run ${runId} and its artifacts will be permanently removed.`}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Back</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={action === 'cancel' ? cancelling : deleting}
            onClick={() => {
              if (action === 'cancel') {
                onConfirmCancel()
              } else if (action === 'delete') {
                onConfirmDelete()
              }
              onOpenChange(false)
            }}
          >
            {action === 'cancel'
              ? (cancelling ? 'Cancelling…' : 'Cancel run')
              : (deleting ? 'Deleting…' : 'Delete run')}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
