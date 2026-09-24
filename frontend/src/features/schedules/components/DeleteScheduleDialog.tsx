import { ConfirmDialog } from '@/shared/components/ConfirmDialog'
import type { useDeleteTarget } from '@/shared/hooks/useDeleteTarget'
import { useDeleteSchedule } from '../hooks'
import type { Schedule } from '../types'

/**
 * The one schedule delete confirmation, shared by the list page, the detail
 * panel, and the detail page. `onDeleted` says where to go afterwards (close
 * the panel, leave the detail page).
 */
export function DeleteScheduleDialog({ target, onDeleted }: {
  target: ReturnType<typeof useDeleteTarget<Schedule>>
  onDeleted: () => void
}) {
  const { mutateAsync: deleteSchedule, isPending } = useDeleteSchedule()
  return (
    <ConfirmDialog
      open={target.open}
      onCancel={target.cancel}
      verb="Delete"
      noun="schedule"
      description={`"${target.target?.name}" will be permanently deleted.`}
      error={target.error}
      pending={isPending}
      onConfirm={() => void target.confirm(async schedule => {
        await deleteSchedule(schedule.id)
        onDeleted()
      })}
    />
  )
}
