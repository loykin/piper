import { ConfirmDialog } from '@/shared/components/ConfirmDialog'
import type { useDeleteTarget } from '@/shared/hooks/useDeleteTarget'
import { useDeleteNotebook } from '../hooks'

/**
 * The one notebook delete confirmation, shared by the list page and the
 * detail panel. The target is the notebook name.
 */
export function DeleteNotebookDialog({ target, onDeleted }: {
  target: ReturnType<typeof useDeleteTarget<string>>
  onDeleted: () => void
}) {
  const { mutateAsync: deleteNotebook, isPending } = useDeleteNotebook()
  return (
    <ConfirmDialog
      open={target.open}
      onCancel={target.cancel}
      verb="Delete"
      noun="notebook"
      description={`"${target.target}" will be deleted. Its volume and work directory are preserved and can be reattached from the Volumes page.`}
      error={target.error}
      pending={isPending}
      onConfirm={() => void target.confirm(async name => {
        await deleteNotebook(name)
        onDeleted()
      })}
    />
  )
}
