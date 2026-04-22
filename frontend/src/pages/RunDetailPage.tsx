import { useEffect, useState, useRef, useCallback } from 'react'
import { useParams, Link } from 'react-router-dom'
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
import { getRun, streamLogs, type Run, type Step, type LogLine } from '../api'
import StatusBadge from '../components/StatusBadge'
import RunDAG from '../components/RunDAG'

function formatStepTime(value?: string): string {
  if (!value) return '—'
  return new Date(value).toLocaleTimeString()
}

function formatStepDuration(step: Step): string {
  if (!step.started_at) return '—'
  const start = new Date(step.started_at).getTime()
  const end = step.ended_at ? new Date(step.ended_at).getTime() : Date.now()
  const ms = Math.max(0, end - start)
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  return `${(ms / 60000).toFixed(1)}m`
}

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
    getRun(id)
      .then(({ run, steps }) => {
        setRun(run)
        setSteps(steps)
        if (!selected && steps.length > 0) {
          setSelected(steps[0].step_name)
        }
      })
      .catch(() => {})
  }, [id, selected])

  useEffect(() => {
    load()
    const iv = setInterval(() => {
      if (run?.status === 'running') load()
    }, 2000)
    return () => clearInterval(iv)
  }, [load, run?.status])

  useEffect(() => {
    if (!id || !selected) return
    esRef.current?.close()
    setLogs([])
    setLogDone(false)

    const es = streamLogs(
      id,
      selected,
      (line) => setLogs(prev => [...prev, line]),
      (status) => {
        setLogDone(true)
        console.log('log stream done:', status)
      },
    )
    esRef.current = es
    return () => es.close()
  }, [id, selected])

  useEffect(() => {
    logEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [logs])

  const columns: DataGridColumnDef<Step>[] = [
    {
      accessorKey: 'step_name',
      header: 'Step',
      meta: { minWidth: 220, flex: 1 },
      cell: ({ row }) => (
        <button
          type="button"
          onClick={() => setSelected(row.original.step_name)}
          className={`text-left ${selected === row.original.step_name ? 'text-indigo-300' : 'text-gray-200 hover:text-white'}`}
        >
          {row.original.step_name}
        </button>
      ),
    },
    {
      accessorKey: 'status',
      header: 'Status',
      meta: { minWidth: 140 },
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      accessorKey: 'started_at',
      header: 'Started',
      meta: { minWidth: 140 },
      cell: ({ row }) => <span className="text-gray-400">{formatStepTime(row.original.started_at)}</span>,
    },
    {
      id: 'duration',
      header: 'Duration',
      meta: { minWidth: 120, align: 'right' },
      cell: ({ row }) => <span className="text-gray-400">{formatStepDuration(row.original)}</span>,
    },
    {
      id: 'error',
      header: 'Error',
      meta: { minWidth: 240, flex: 1 },
      cell: ({ row }) => (
        <span className="block truncate text-xs text-red-400">{row.original.error ?? '—'}</span>
      ),
    },
  ]

  if (!run) return <p className="text-sm text-gray-500">Loading…</p>

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-3">
        <Link to="/" className="text-sm text-gray-500 hover:text-gray-300">← Runs</Link>
        <span className="text-gray-600">/</span>
        <span className="font-mono text-sm text-gray-300">{run.id}</span>
        <StatusBadge status={run.status} />
      </div>

      {/* DAG */}
      <RunDAG
        pipelineYaml={run.pipeline_yaml}
        steps={steps}
        selected={selected}
        onSelectStep={setSelected}
      />

      {/* Steps table */}
      <section className="overflow-hidden rounded-xl border border-gray-800 bg-gray-950">
        <div className="border-b border-gray-800 bg-gray-900 px-4 py-3">
          <h2 className="text-sm font-semibold uppercase tracking-wider text-gray-300">Steps</h2>
        </div>
        <DataGrid
          data={steps}
          columns={columns}
          tableHeight="auto"
          rowHeight={44}
          rowCursor
          onRowClick={(row) => setSelected(row.step_name)}
          pagination={{ pageSize: 10 }}
          footer={(table) => <DataGridPaginationBar table={table} pageSizes={[10, 20, 50]} />}
          classNames={{
            container: 'border-0 rounded-none bg-transparent',
            header: 'bg-gray-900',
            headerCell: 'text-xs uppercase tracking-wider text-gray-400',
            row: 'bg-gray-950 hover:bg-gray-900 transition-colors',
            cell: 'text-sm text-gray-200',
          }}
        />
      </section>

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
              {steps.find(step => step.step_name === selected)?.error && (
                <span className="text-xs text-red-400">
                  {steps.find(step => step.step_name === selected)?.error}
                </span>
              )}
            </div>

            <div className="h-[520px] overflow-y-auto rounded-xl border border-gray-800 bg-gray-950 p-4 font-mono text-xs leading-5">
              {logs.length === 0 && (
                <span className="text-gray-600">{logDone ? 'No output.' : 'Waiting for logs…'}</span>
              )}
              {logs.map((line, index) => (
                <div
                  key={index}
                  className={`flex gap-3 ${line.stream === 'stderr' ? 'text-red-400' : 'text-gray-300'}`}
                >
                  <span className="w-20 shrink-0 select-none text-gray-600">
                    {new Date(line.ts).toLocaleTimeString()}
                  </span>
                  <span className="break-all">{line.line}</span>
                </div>
              ))}
              <div ref={logEndRef} />
            </div>
          </>
        )}
      </div>
    </div>
  )
}
