import { useEffect, useState, useRef } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { listRuns, createRun, type Run } from '../api'
import StatusBadge from '../components/StatusBadge'

const EXAMPLE_YAML = `apiVersion: piper/v1
kind: Pipeline
metadata:
  name: my-pipeline
spec:
  steps:
    - name: hello
      run:
        command: [echo, "hello from piper"]
    - name: world
      depends_on: [hello]
      run:
        command: [echo, "world!"]
`

function elapsed(run: Run): string {
  const start = new Date(run.started_at).getTime()
  const end = run.finished_at ? new Date(run.finished_at).getTime() : Date.now()
  const ms = end - start
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  return `${(ms / 60000).toFixed(1)}m`
}

export default function RunsPage() {
  const [runs, setRuns] = useState<Run[]>([])
  const [loading, setLoading] = useState(true)
  const [yaml, setYaml] = useState(EXAMPLE_YAML)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const navigate = useNavigate()
  const intervalRef = useRef<ReturnType<typeof setInterval>>(0 as unknown as ReturnType<typeof setInterval>)

  const load = () =>
    listRuns()
      .then(setRuns)
      .catch(() => {})
      .finally(() => setLoading(false))

  useEffect(() => {
    load()
    intervalRef.current = setInterval(load, 3000)
    return () => clearInterval(intervalRef.current!)

  }, [])

  async function handleRun() {
    setSubmitting(true)
    setError('')
    try {
      const { run_id } = await createRun(yaml)
      navigate(`/runs/${run_id}`)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="space-y-8">
      {/* 실행 패널 */}
      <section className="rounded-xl border border-gray-800 bg-gray-900 p-6">
        <h2 className="mb-4 text-sm font-semibold text-gray-300 uppercase tracking-wider">New Run</h2>
        <textarea
          className="w-full rounded-lg border border-gray-700 bg-gray-950 p-3 font-mono text-sm text-gray-200 focus:border-indigo-500 focus:outline-none resize-none"
          rows={10}
          value={yaml}
          onChange={e => setYaml(e.target.value)}
          spellCheck={false}
        />
        {error && <p className="mt-2 text-sm text-red-400">{error}</p>}
        <div className="mt-3 flex justify-end">
          <button
            onClick={handleRun}
            disabled={submitting}
            className="rounded-lg bg-indigo-600 px-5 py-2 text-sm font-semibold text-white hover:bg-indigo-500 disabled:opacity-50 transition-colors"
          >
            {submitting ? 'Submitting…' : '▶ Run Pipeline'}
          </button>
        </div>
      </section>

      {/* 실행 목록 */}
      <section>
        <h2 className="mb-4 text-sm font-semibold text-gray-300 uppercase tracking-wider">
          Recent Runs
        </h2>
        {loading ? (
          <p className="text-sm text-gray-500">Loading…</p>
        ) : runs.length === 0 ? (
          <p className="text-sm text-gray-500">No runs yet.</p>
        ) : (
          <div className="overflow-hidden rounded-xl border border-gray-800">
            <table className="w-full text-sm">
              <thead className="bg-gray-900 text-left text-xs text-gray-400 uppercase tracking-wider">
                <tr>
                  <th className="px-4 py-3">Run ID</th>
                  <th className="px-4 py-3">Pipeline</th>
                  <th className="px-4 py-3">Status</th>
                  <th className="px-4 py-3">Started</th>
                  <th className="px-4 py-3">Duration</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-800 bg-gray-950">
                {runs.map(run => (
                  <tr key={run.id} className="hover:bg-gray-900 transition-colors">
                    <td className="px-4 py-3 font-mono text-xs">
                      <Link to={`/runs/${run.id}`} className="text-indigo-400 hover:text-indigo-300">
                        {run.id}
                      </Link>
                    </td>
                    <td className="px-4 py-3 text-gray-300">{run.pipeline_name}</td>
                    <td className="px-4 py-3"><StatusBadge status={run.status} /></td>
                    <td className="px-4 py-3 text-gray-400">
                      {new Date(run.started_at).toLocaleString()}
                    </td>
                    <td className="px-4 py-3 text-gray-400">{elapsed(run)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  )
}
