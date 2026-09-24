import { LIVE_STATUSES, statusTone, toneBadge } from '@/shared/status'

export default function StatusBadge({ status }: { status: string }) {
  return (
    <span className={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium ${toneBadge[statusTone(status)]}`}>
      {LIVE_STATUSES.has(status) && <span className="mr-1 h-1.5 w-1.5 rounded-full bg-current animate-pulse" />}
      {status}
    </span>
  )
}
