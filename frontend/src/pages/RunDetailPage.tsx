import { useEffect, useState, useRef, useCallback } from 'react'
import { useParams, Link } from 'react-router-dom'
import { getRun, streamLogs, type Run, type Step, type LogLine } from '../api'
import StatusBadge from '../components/StatusBadge'

export default function RunDetailPage() {
  const { id } = useParams<{ id: string }>()
  const [run, setRun] = useState<Run | null>(null)
  const [steps, setSteps] = useState<Step[]>([])
  const [selected, setSelected] = useState<string | null>(null)
  const [logs, setLogs] = useState<LogLine[]>([])
  const [logDone, setLogDone] = useState(false)
  const logEndRef = useRef<HTMLDivElement>(null)
  const esRef = useRef<EventSource | null>(null)

  const load = useCallback(() => {
    if (!id) return
    getRun(id).then(({ run, steps }) => {
      setRun(run)
      setSteps(steps)
      if (!selected && steps.length > 0) {
        setSelected(steps[0].step_name)
      }
    }).catch(() => {})
  }, [id, selected])

  // run 폴링 (running 상태일 때)
  useEffect(() => {
    load()
    const iv = setInterval(() => {
      if (run?.status === 'running') load()
    }, 2000)
    return () => clearInterval(iv)
  }, [load, run?.status])

  // 선택된 step 로그 스트리밍
  useEffect(() => {
    if (!id || !selected) return
    esRef.current?.close()
    setLogs([])
    setLogDone(false)

    const es = streamLogs(
      id,
      selected,
      (line) => setLogs(prev => [...prev, line]),
      (status) => { setLogDone(true); console.log('log stream done:', status) },
    )
    esRef.current = es
    return () => es.close()
  }, [id, selected])

  // 로그 자동 스크롤
  useEffect(() => {
    logEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [logs])

  if (!run) return <p className="text-gray-500 text-sm">Loading…</p>

  return (
    <div className="space-y-6">
      {/* 헤더 */}
      <div className="flex items-center gap-3">
        <Link to="/" className="text-gray-500 hover:text-gray-300 text-sm">← Runs</Link>
        <span className="text-gray-600">/</span>
        <span className="font-mono text-sm text-gray-300">{run.id}</span>
        <StatusBadge status={run.status} />
      </div>

      <div className="grid grid-cols-[220px_1fr] gap-6">
        {/* Step 목록 (사이드바) */}
        <aside className="rounded-xl border border-gray-800 bg-gray-900 p-3 h-fit">
          <p className="mb-3 text-xs font-semibold text-gray-500 uppercase tracking-wider px-2">Steps</p>
          <ul className="space-y-1">
            {steps.map(step => (
              <li key={step.step_name}>
                <button
                  onClick={() => setSelected(step.step_name)}
                  className={`w-full flex items-center justify-between rounded-lg px-3 py-2 text-sm transition-colors ${
                    selected === step.step_name
                      ? 'bg-indigo-600/20 text-indigo-300'
                      : 'text-gray-400 hover:bg-gray-800 hover:text-gray-200'
                  }`}
                >
                  <span className="truncate">{step.step_name}</span>
                  <StatusBadge status={step.status} />
                </button>
              </li>
            ))}
          </ul>
        </aside>

        {/* 로그 뷰어 */}
        <div className="flex flex-col gap-3">
          {selected && (
            <>
              <div className="flex items-center justify-between">
                <h3 className="text-sm font-semibold text-gray-300">
                  {selected}
                  {!logDone && (
                    <span className="ml-2 inline-block h-2 w-2 rounded-full bg-blue-400 animate-pulse" />
                  )}
                </h3>
                {steps.find(s => s.step_name === selected)?.error && (
                  <span className="text-xs text-red-400">
                    {steps.find(s => s.step_name === selected)?.error}
                  </span>
                )}
              </div>

              <div className="h-[520px] overflow-y-auto rounded-xl border border-gray-800 bg-gray-950 p-4 font-mono text-xs leading-5">
                {logs.length === 0 && (
                  <span className="text-gray-600">{logDone ? 'No output.' : 'Waiting for logs…'}</span>
                )}
                {logs.map((l, i) => (
                  <div key={i} className={`flex gap-3 ${l.stream === 'stderr' ? 'text-red-400' : 'text-gray-300'}`}>
                    <span className="shrink-0 text-gray-600 select-none w-20">
                      {new Date(l.ts).toLocaleTimeString()}
                    </span>
                    <span className="break-all">{l.line}</span>
                  </div>
                ))}
                <div ref={logEndRef} />
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
