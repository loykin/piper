import { Button } from '@loykin/designkit'
import { ApiError } from '@/lib/api'

interface QueryErrorNoticeProps {
  message: string
  error?: unknown
  onRetry?: () => void
}

/** True when the backend reported a Federation Member as unreachable (fed.md §13). */
function isMemberUnavailable(error: unknown): boolean {
  return error instanceof ApiError && error.status === 503 && error.message.includes('member unavailable')
}

function isStatsUnavailable(error: unknown): boolean {
  return error instanceof ApiError && error.status === 503 && error.message.includes('statistics backend unavailable')
}

export function QueryErrorNotice({ message, error, onRetry }: QueryErrorNoticeProps) {
  const detail = error instanceof Error && error.message ? error.message : ''
  const memberUnavailable = isMemberUnavailable(error)
  const statsUnavailable = isStatsUnavailable(error)

  const degraded = memberUnavailable || statsUnavailable

  return (
    <div
      className={
        degraded
          ? 'mb-4 flex items-center justify-between gap-3 rounded-md border border-amber-500/30 bg-amber-500/5 px-3 py-2'
          : 'mb-4 flex items-center justify-between gap-3 rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2'
      }
    >
      <p className={degraded ? 'text-sm text-amber-700 dark:text-amber-400' : 'text-sm text-destructive'}>
        {memberUnavailable
          ? 'Member disconnected — waiting to reconnect. No data has been lost.'
          : statsUnavailable
            ? 'Statistics backend unavailable — new data remains queued on the owning Member.'
          : `${message}${detail ? `: ${detail}` : '.'}`}
      </p>
      {onRetry && (
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-8 shrink-0 text-xs"
          onClick={onRetry}
        >
          Retry
        </Button>
      )}
    </div>
  )
}
