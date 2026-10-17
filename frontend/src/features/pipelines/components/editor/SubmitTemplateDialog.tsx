import { Upload } from 'lucide-react'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import type { NotebookVolume } from '@/features/notebooks/types'
import { DialogSubmitFooter } from '@/shared/components/DialogSubmitFooter'

/**
 * Final confirmation before a pipeline template version is snapshotted. The
 * source workspace was chosen once, before editing; this only shows it.
 */
export function SubmitTemplateDialog({
  open, workspace, error, submitting, onClose, onConfirm,
}: {
  open: boolean
  /** The editor's locked source volume, or null for git / commands-only. */
  workspace: NotebookVolume | null
  error?: string
  submitting: boolean
  onClose: () => void
  onConfirm: () => void
}) {
  return (
    <Dialog open={open} onOpenChange={next => { if (!next) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Submit Pipeline Template</DialogTitle>
          <DialogDescription>
            Submitting creates a new immutable snapshot of your source files in object storage.
            Each submission gets its own UUID — previous snapshots are untouched.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          {workspace && (
            <p className="min-w-0 break-all text-sm text-muted-foreground">
              Snapshot from <span className="font-medium text-foreground">{workspace.label}</span> · {workspace.work_dir}
            </p>
          )}
          {error && <p className="text-sm text-destructive">{error}</p>}
        </div>
        <DialogSubmitFooter verb="Submit" noun="Template" icon={<Upload />} pending={submitting} onCancel={onClose} onSubmit={onConfirm} />
      </DialogContent>
    </Dialog>
  )
}
