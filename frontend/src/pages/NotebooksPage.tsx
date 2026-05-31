import { useEffect, useRef, useState } from 'react'
import { DataGrid, DataGridPaginationCompact, type DataGridColumnDef } from '@loykin/gridkit'
import {
  DataBodyTemplate, DataPage,
  Select, SelectTrigger, SelectContent, SelectItem, SelectValue,
} from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { listNotebooks, createNotebook, deleteNotebook, notebookProxyURL, listNotebookWorkers, type NotebookServer, type NotebookWorkerInfo } from '../api'
import StatusBadge from '../components/StatusBadge'

// ─── Form ─────────────────────────────────────────────────────────────────────

interface FormState {
  name: string
  mode: string
  port: string
  workDir: string
  gpus: string
  worker: string
  k8sNamespace: string
  k8sImage: string
}

const DEFAULT_FORM: FormState = {
  name: '',
  mode: 'local',
  port: '8888',
  workDir: './notebooks',
  gpus: '',
  worker: '',
  k8sNamespace: 'default',
  k8sImage: 'jupyter/scipy-notebook:latest',
}

const JUPYTER_IMAGES = [
  'jupyter/scipy-notebook:latest',
  'jupyter/tensorflow-notebook:latest',
  'jupyter/pytorch-notebook:latest',
  'jupyter/datascience-notebook:latest',
  'jupyter/base-notebook:latest',
]

function buildYAML(f: FormState): string {
  const isK8s = f.mode === 'k8s'
  const gpuLine = !isK8s && f.gpus ? `\n    gpus: "${f.gpus}"` : ''
  const workerLine = !isK8s && f.worker ? `\n    worker: ${f.worker}` : ''
  const k8sSection = isK8s ? `  k8s:
    namespace: ${f.k8sNamespace || 'default'}
    image: ${f.k8sImage || 'jupyter/scipy-notebook:latest'}
` : ''

  return `apiVersion: piper/v1
kind: NotebookServer
metadata:
  name: ${f.name || 'my-notebook'}
spec:
  runtime:
    mode: ${f.mode}
    port: ${f.port || '8888'}
    work_dir: ${f.workDir || './notebooks'}${gpuLine}${workerLine}
${k8sSection}`
}

function LaunchPanel({ onClose, onLaunched }: { onClose: () => void; onLaunched: () => void }) {
  const [form, setForm] = useState<FormState>(DEFAULT_FORM)
  const [tab, setTab] = useState<'form' | 'yaml'>('form')
  const [yaml, setYaml] = useState(() => buildYAML(DEFAULT_FORM))
  const [launching, setLaunching] = useState(false)
  const [error, setError] = useState('')
  const [notebookWorkers, setNotebookWorkers] = useState<NotebookWorkerInfo[]>([])

  useEffect(() => {
    listNotebookWorkers().then(setNotebookWorkers).catch(() => {})
  }, [])

  function setField<K extends keyof FormState>(key: K, value: FormState[K]) {
    setForm(prev => {
      const next = { ...prev, [key]: value }
      setYaml(buildYAML(next))
      return next
    })
  }

  async function handleLaunch() {
    setError('')
    const payload = tab === 'form' ? buildYAML(form) : yaml
    if (!payload.trim()) { setError('YAML is required.'); return }
    try {
      setLaunching(true)
      await createNotebook(payload.trim())
      onLaunched()
      onClose()
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setLaunching(false)
    }
  }

  return (
    <DataPage.Group surface="bordered" className="mb-4">
      <div className="p-4">
        <div className="mb-4 flex items-center justify-between">
          <span className="text-sm font-semibold">Launch Notebook Server</span>
          <div className="flex items-center gap-2">
            <div className="flex overflow-hidden rounded border border-border text-xs">
              <button type="button" onClick={() => setTab('form')}
                className={`px-3 py-1.5 transition-colors ${tab === 'form' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:text-foreground'}`}>
                Form
              </button>
              <button type="button" onClick={() => { setYaml(buildYAML(form)); setTab('yaml') }}
                className={`px-3 py-1.5 transition-colors ${tab === 'yaml' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:text-foreground'}`}>
                YAML
              </button>
            </div>
            <button type="button" onClick={onClose}
              className="text-xs text-muted-foreground hover:text-foreground">✕</button>
          </div>
        </div>

        {tab === 'form' ? (
          <div className="space-y-4">
            <DataBodyTemplate.Field label="Server Name">
              <Input
                value={form.name}
                onChange={e => setField('name', e.target.value)}
                placeholder="my-notebook"
              />
            </DataBodyTemplate.Field>

            <DataBodyTemplate.Group layout="stacked" variant="bordered" title="Runtime">
              <div className="grid grid-cols-3 gap-3">
                <DataBodyTemplate.Field label="Mode">
                  <Select value={form.mode} onValueChange={v => setField('mode', v)}>
                    <SelectTrigger size="sm">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="local">local</SelectItem>
                      <SelectItem value="k8s">k8s</SelectItem>
                    </SelectContent>
                  </Select>
                </DataBodyTemplate.Field>
                <DataBodyTemplate.Field label="Port">
                  <Input value={form.port} onChange={e => setField('port', e.target.value)} placeholder="8888" />
                </DataBodyTemplate.Field>
                <DataBodyTemplate.Field label="Work Directory">
                  <Input value={form.workDir} onChange={e => setField('workDir', e.target.value)} placeholder="./notebooks" />
                </DataBodyTemplate.Field>
              </div>

              {form.mode === 'local' && (
                <>
                  <DataBodyTemplate.Field label="GPUs" description="e.g. 0 · 0,1 · all · none · blank = host default">
                    <Input value={form.gpus} onChange={e => setField('gpus', e.target.value)} placeholder="0" />
                  </DataBodyTemplate.Field>
                  <DataBodyTemplate.Field label="Worker" description="Launch on a specific worker node. Leave blank to auto-assign.">
                    <Select value={form.worker} onValueChange={v => setField('worker', v)}>
                      <SelectTrigger size="sm"><SelectValue placeholder="— auto assign —" /></SelectTrigger>
                      <SelectContent>
                        <SelectItem value="">auto assign</SelectItem>
                        {notebookWorkers.map(w => (
                          <SelectItem key={w.id} value={w.hostname}>
                            {w.hostname}{w.gpus?.length ? ` (GPU: ${w.gpus.join(', ')})` : ''}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </DataBodyTemplate.Field>
                </>
              )}

              {form.mode === 'k8s' && (
                <div className="grid grid-cols-2 gap-3">
                  <DataBodyTemplate.Field label="Namespace">
                    <Input value={form.k8sNamespace} onChange={e => setField('k8sNamespace', e.target.value)} placeholder="default" />
                  </DataBodyTemplate.Field>
                  <DataBodyTemplate.Field label="Image">
                    <Input
                      list="jupyter-images"
                      value={form.k8sImage}
                      onChange={e => setField('k8sImage', e.target.value)}
                      placeholder="jupyter/scipy-notebook:latest"
                    />
                    <datalist id="jupyter-images">
                      {JUPYTER_IMAGES.map(img => <option key={img} value={img} />)}
                    </datalist>
                  </DataBodyTemplate.Field>
                </div>
              )}
            </DataBodyTemplate.Group>
          </div>
        ) : (
          <textarea
            className="w-full resize-none rounded border border-border bg-background p-3 font-mono text-sm focus:outline-none"
            rows={16}
            value={yaml}
            onChange={e => setYaml(e.target.value)}
            spellCheck={false}
          />
        )}

        {error && <p className="mt-3 text-sm text-destructive">{error}</p>}

        <div className="mt-4 flex justify-end gap-2">
          <Button variant="outline" size="sm" onClick={onClose}>Cancel</Button>
          <Button size="sm" onClick={() => void handleLaunch()} disabled={launching}>
            {launching ? 'Launching…' : 'Launch'}
          </Button>
        </div>
      </div>
    </DataPage.Group>
  )
}

// ─── Main page ────────────────────────────────────────────────────────────────

export default function NotebooksPage() {
  const [notebooks, setNotebooks] = useState<NotebookServer[]>([])
  const [loading, setLoading] = useState(true)
  const [showLaunch, setShowLaunch] = useState(false)
  const [deleting, setDeleting] = useState<string | null>(null)
  const intervalRef = useRef<ReturnType<typeof setInterval>>(0 as unknown as ReturnType<typeof setInterval>)

  const load = () =>
    listNotebooks()
      .then(setNotebooks)
      .catch(() => {})
      .finally(() => setLoading(false))

  useEffect(() => {
    load()
    intervalRef.current = setInterval(load, 5000)
    return () => clearInterval(intervalRef.current)
  }, [])

  const handleDelete = async (name: string) => {
    if (!confirm(`Stop and delete notebook "${name}"?`)) return
    setDeleting(name)
    try {
      await deleteNotebook(name)
      await load()
    } catch (err) {
      alert(err instanceof Error ? err.message : String(err))
    } finally {
      setDeleting(null)
    }
  }

  const columns: DataGridColumnDef<NotebookServer>[] = [
    {
      accessorKey: 'name',
      header: 'Name',
      meta: { minWidth: 160 },
      cell: ({ row }) => <span className="font-medium">{row.original.name}</span>,
    },
    {
      accessorKey: 'status',
      header: 'Status',
      meta: { minWidth: 110 },
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      accessorKey: 'work_dir',
      header: 'Work Dir',
      meta: { minWidth: 180, flex: 1 },
      cell: ({ row }) => (
        <span className="font-mono text-xs text-muted-foreground">{row.original.work_dir || '—'}</span>
      ),
    },
    {
      id: 'pid',
      header: 'PID',
      meta: { minWidth: 80 },
      cell: ({ row }) => (
        <span className="font-mono text-xs text-muted-foreground">
          {row.original.pid > 0 ? row.original.pid : '—'}
        </span>
      ),
    },
    {
      accessorKey: 'created_at',
      header: 'Started',
      meta: { minWidth: 160 },
      cell: ({ row }) => (
        <span className="text-xs text-muted-foreground">
          {new Date(row.original.created_at).toLocaleString()}
        </span>
      ),
    },
    {
      id: 'actions',
      header: '',
      meta: { minWidth: 120, align: 'right' },
      cell: ({ row }) => (
        <div className="flex justify-end gap-1">
          {row.original.status === 'running' && (
            <a
              href={notebookProxyURL(row.original.name, row.original.token)}
              target="_blank"
              rel="noreferrer"
              onClick={e => e.stopPropagation()}
              className="rounded px-2 py-1 text-xs text-primary hover:bg-primary/10"
            >
              Open
            </a>
          )}
          <Button
            variant="ghost"
            size="xs"
            disabled={deleting === row.original.name}
            onClick={e => { e.stopPropagation(); void handleDelete(row.original.name) }}
            className="text-destructive hover:bg-destructive/10 hover:text-destructive"
          >
            {deleting === row.original.name ? '…' : 'Delete'}
          </Button>
        </div>
      ),
    },
  ]

  return (
    <DataPage>
      <DataPage.Header>
        <DataPage.TitleBlock
          title="Notebooks"
          description="Jupyter notebook servers. Click Open to launch in a new tab."
        />
        {!showLaunch && (
          <DataPage.Actions>
            <Button size="sm" onClick={() => setShowLaunch(true)}>Launch</Button>
          </DataPage.Actions>
        )}
      </DataPage.Header>

      <DataPage.Content>
        {showLaunch && (
          <LaunchPanel onClose={() => setShowLaunch(false)} onLaunched={load} />
        )}

        {loading ? (
          <div className="py-8 text-sm text-muted-foreground">Loading…</div>
        ) : notebooks.length === 0 ? (
          <div className="py-12 text-center">
            <p className="text-sm text-muted-foreground">No notebook servers running.</p>
            <p className="mt-1 text-xs text-muted-foreground/60">Launch a Jupyter server to start developing.</p>
          </div>
        ) : (
          <DataPage.Group surface="none" className="h-full">
            <DataPage.GroupBody className="h-full [&_.dg-shell]:h-full [&_.dg-table-wrapper]:min-h-0 [&_.dg-table-wrapper]:flex-1">
              <DataGrid
                data={notebooks}
                columns={columns}
                tableWidthMode="fill-last"
                rowHeight={44}
                pagination={{ pageSize: 20 }}
                footer={(table) => (
                  <div className="flex h-9 items-center justify-between px-1 text-xs text-muted-foreground">
                    <span>{notebooks.length} servers</span>
                    <DataGridPaginationCompact table={table} />
                  </div>
                )}
              />
            </DataPage.GroupBody>
          </DataPage.Group>
        )}
      </DataPage.Content>
    </DataPage>
  )
}
