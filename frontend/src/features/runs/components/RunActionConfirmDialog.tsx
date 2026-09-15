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

export type RunConfirmVerb = 'cancel'

interface RunActionConfirmDialogProps {
  runId: string
  action: RunConfirmVerb | null
  onOpenChange: (open: boolean) => void
  cancelling: boolean
  onConfirmCancel: () => void
}

// Cancel confirmation for a run — identical AlertDialog copy and behavior
// between the full RunDetailPage and the RunDetailPanel side-panel view.
export function RunActionConfirmDialog({
  runId,
  action,
  onOpenChange,
  cancelling,
  onConfirmCancel,
}: RunActionConfirmDialogProps) {
  return (
    <AlertDialog open={action != null} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Cancel this run?</AlertDialogTitle>
          <AlertDialogDescription>
            Run {runId} will be stopped immediately.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Back</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={cancelling}
            onClick={() => {
              onConfirmCancel()
              onOpenChange(false)
            }}
          >
            {cancelling ? 'Cancelling…' : 'Cancel run'}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
