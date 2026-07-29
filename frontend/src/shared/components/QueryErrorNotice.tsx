import { Button } from '@loykin/designkit'

interface QueryErrorNoticeProps {
  message: string
  error?: unknown
  onRetry?: () => void
}

export function QueryErrorNotice({ message, error, onRetry }: QueryErrorNoticeProps) {
  const detail = error instanceof Error && error.message ? error.message : ''

  return (
    <div className="mb-4 flex items-center justify-between gap-3 rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2">
      <p className="text-sm text-destructive">
        {message}{detail ? `: ${detail}` : '.'}
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
