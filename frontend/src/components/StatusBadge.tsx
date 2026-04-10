const colors: Record<string, string> = {
  running: 'bg-blue-500/20 text-blue-300 border-blue-500/30',
  success: 'bg-green-500/20 text-green-300 border-green-500/30',
  done:    'bg-green-500/20 text-green-300 border-green-500/30',
  failed:  'bg-red-500/20 text-red-300 border-red-500/30',
  pending: 'bg-gray-500/20 text-gray-400 border-gray-500/30',
  skipped: 'bg-yellow-500/20 text-yellow-300 border-yellow-500/30',
}

export default function StatusBadge({ status }: { status: string }) {
  return (
    <span className={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium ${colors[status] ?? colors.pending}`}>
      {status === 'running' && <span className="mr-1 h-1.5 w-1.5 rounded-full bg-blue-400 animate-pulse" />}
      {status}
    </span>
  )
}
