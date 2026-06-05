// runs feature — Log viewer component
import { useEffect, useRef, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { useRunLogs } from '../hooks'

interface LogViewerProps {
  runId: string
  stepId: string | null
}

export function LogViewer({ runId, stepId }: LogViewerProps) {
  const { lines, done } = useRunLogs(runId, stepId)
  const [autoScroll, setAutoScroll] = useState(true)
  const [streamFilter, setStreamFilter] = useState<'all' | 'stdout' | 'stderr'>('all')
  const [logSearch, setLogSearch] = useState('')
  const logEndRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!autoScroll) return
    logEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [lines, autoScroll])

  const visibleLogs = lines.filter((line) => {
    if (streamFilter !== 'all' && line.stream !== streamFilter) return false
    return !(logSearch && !line.line.toLowerCase().includes(logSearch.toLowerCase()))
  })

  if (!stepId) return null

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-foreground">
          {stepId}
          {!done && (
            <span className="ml-2 inline-block h-2 w-2 rounded-full bg-blue-400 animate-pulse" />
          )}
        </h3>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex overflow-hidden rounded border border-border text-xs">
          {(['all', 'stdout', 'stderr'] as const).map(s => (
            <Button
              key={s}
              type="button"
              size="sm"
              variant={streamFilter === s ? 'default' : 'ghost'}
              onClick={() => setStreamFilter(s)}
              className="h-7 rounded-none border-0 px-2.5 text-xs"
            >
              {s === 'all' ? 'All streams' : s}
            </Button>
          ))}
        </div>
        <Input
          value={logSearch}
          onChange={(e) => setLogSearch(e.target.value)}
          placeholder="Search logs"
          className="h-7 min-w-56 text-xs"
        />
        <label className="ml-auto inline-flex items-center gap-2 text-xs text-muted-foreground">
          <Switch checked={autoScroll} onCheckedChange={setAutoScroll} />
          Auto-scroll
        </label>
      </div>

      <div className="h-130 overflow-y-auto rounded-xl border border-border bg-muted/20 p-4 font-mono text-xs leading-5">
        {visibleLogs.length === 0 && (
          <span className="text-muted-foreground">{done ? 'No output.' : 'Waiting for logs…'}</span>
        )}
        {visibleLogs.map((line, index) => (
          <div
            key={index}
            className={`flex gap-3 ${line.stream === 'stderr' ? 'text-red-400' : 'text-foreground'}`}
          >
            <span className="w-20 shrink-0 select-none text-muted-foreground">
              {new Date(line.ts).toLocaleTimeString()}
            </span>
            <span className="break-all">{line.line}</span>
          </div>
        ))}
        <div ref={logEndRef} />
      </div>
    </div>
  )
}
