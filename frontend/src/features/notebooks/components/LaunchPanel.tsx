import { useEffect, useState } from 'react'
import {
  DataBodyTemplate, DataPage,
  Select, SelectTrigger, SelectContent, SelectItem, SelectValue,
} from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { createNotebook, listNotebookWorkers, type NotebookVolume, type NotebookWorkerInfo } from '../api'

interface FormState {
  name: string
  gpus: string
  worker: string
}

const DEFAULT_FORM: FormState = { name: '', gpus: '', worker: '' }

function buildYAML(f: FormState): string {
  const gpuLine = f.gpus ? `\n  gpus: "${f.gpus}"` : ''
  const workerLine = f.worker ? `\n  worker: ${f.worker}` : ''
  return `apiVersion: piper/v1
kind: NotebookServer
metadata:
  name: ${f.name || 'my-notebook'}
spec:${gpuLine}${workerLine}
`
}

interface LaunchPanelProps {
  onClose: () => void
  onLaunched: () => void
  initialVolumeId?: string          // pre-select a specific released volume
  releasedVolumes: NotebookVolume[] // list for the dropdown
}

export default function LaunchPanel({ onClose, onLaunched, initialVolumeId, releasedVolumes }: LaunchPanelProps) {
  const [form, setForm] = useState<FormState>(DEFAULT_FORM)
  const [tab, setTab] = useState<'form' | 'yaml'>('form')
  const [yaml, setYaml] = useState(() => buildYAML(DEFAULT_FORM))
  const [volumeId, setVolumeId] = useState(initialVolumeId ?? '')
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
    if (tab === 'form' && !form.name.trim()) { setError('Server name is required.'); return }
    const payload = tab === 'form' ? buildYAML(form) : yaml
    if (!payload.trim()) { setError('YAML is required.'); return }
    try {
      setLaunching(true)
      await createNotebook(payload.trim(), volumeId || undefined)
      onLaunched()
      onClose()
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setLaunching(false)
    }
  }

  const selectedVol = releasedVolumes.find(v => v.id === volumeId)

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
                autoFocus
              />
            </DataBodyTemplate.Field>

            <DataBodyTemplate.Field
              label="Volume"
              description="Attach to a released volume to recover existing data, or leave blank to provision a new one.">
              <Select value={volumeId} onValueChange={setVolumeId}>
                <SelectTrigger size="sm">
                  <SelectValue placeholder="— new volume —" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="">new volume</SelectItem>
                  {releasedVolumes.map(v => (
                    <SelectItem key={v.id} value={v.id}>
                      {v.label}&nbsp;·&nbsp;
                      <span className="font-mono text-xs">{v.id.slice(0, 8)}</span>
                      {v.work_dir ? `  ${v.work_dir}` : ''}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {selectedVol && (
                <p className="mt-1 font-mono text-xs text-muted-foreground">
                  {selectedVol.work_dir}
                </p>
              )}
            </DataBodyTemplate.Field>

            <DataBodyTemplate.Field label="GPUs" description="e.g. 0 · 0,1 · all · leave blank for no GPU">
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
          </div>
        ) : (
          <textarea
            className="w-full resize-none rounded border border-border bg-background p-3 font-mono text-sm focus:outline-none"
            rows={12}
            value={yaml}
            onChange={e => setYaml(e.target.value)}
            spellCheck={false}
          />
        )}

        {error && <p className="mt-3 text-sm text-destructive">{error}</p>}

        <div className="mt-4 flex justify-end gap-2">
          <Button variant="outline" size="sm" onClick={onClose}>Cancel</Button>
          <Button size="sm" onClick={() => void handleLaunch()} disabled={launching}>
            {launching ? 'Launching…' : volumeId ? 'Attach & Launch' : 'Launch'}
          </Button>
        </div>
      </div>
    </DataPage.Group>
  )
}
