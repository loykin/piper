// serving feature — Deploy form component
import { useEffect, useState } from 'react'
import {
  DataBodyTemplate, DataPage,
  Select, SelectTrigger, SelectContent, SelectItem, SelectValue,
} from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { YamlMirror } from '@/components/ui/yaml-mirror'
import { EnvVarEditor } from '@/shared/components/EnvVarEditor'
import { emptyEnvVarDraft, type EnvVarDraft } from '@/shared/env'
import { useRuns } from '@/features/runs/hooks'
import { listArtifacts, type StepArtifacts } from '@/features/runs/api'
import { useCreateService, useServingWorkers } from '../hooks'
import { useProjectId } from '@/lib/projectContext'
import { buildYAML, DEFAULT_FORM, RUNTIME_TEMPLATES, type FormState } from '../editor'

interface DeployFormProps {
  onClose: () => void
  onDeployed: () => void
}

export function DeployForm({ onClose, onDeployed }: DeployFormProps) {
  const projectId = useProjectId()
  const { data: allRuns = [] } = useRuns({ status: 'success' })
  const { data: servingWorkers = [] } = useServingWorkers()
  const { mutateAsync: deploy, isPending: deploying } = useCreateService()

  const [form, setForm] = useState<FormState>(DEFAULT_FORM)
  const [tab, setTab] = useState<'form' | 'yaml'>('form')
  const [yaml, setYaml] = useState(() => buildYAML(DEFAULT_FORM))
  const [error, setError] = useState('')
  const [artifacts, setArtifacts] = useState<StepArtifacts[]>([])

  const pipelines = Array.from(new Set(allRuns.map(r => r.pipeline_name))).sort()
  const pipelineRuns = allRuns.filter(r => r.pipeline_name === form.pipeline)

  useEffect(() => {
    if (!form.pipeline) { setArtifacts([]); return }
    const runId = form.run === 'latest' ? pipelineRuns[0]?.id : form.run
    if (!runId) { setArtifacts([]); return }
    listArtifacts(projectId, runId).then(setArtifacts).catch(() => setArtifacts([]))
  }, [form.pipeline, form.run, pipelineRuns[0]?.id])

  const steps = artifacts.map(sa => sa.step)
  const artifactNames = artifacts.find(sa => sa.step === form.step)?.artifacts.map(a => a.name) ?? []

  function setField<K extends keyof FormState>(key: K, value: FormState[K]) {
    setForm(prev => {
      const next = { ...prev, [key]: value }
      if (key === 'pipeline') { next.run = 'latest'; next.step = ''; next.artifact = '' }
      if (key === 'run') { next.step = ''; next.artifact = '' }
      if (key === 'step') { next.artifact = '' }
      if (key === 'command' || key === 'port' || key === 'healthPath') next.templateKey = 'custom'
      setYaml(buildYAML(next))
      return next
    })
  }

  function addEnv() {
    setField('env', [...form.env, emptyEnvVarDraft()])
  }

  function updateEnv(rowIndex: number, patch: Partial<EnvVarDraft>) {
    setField('env', form.env.map((item, i) => i === rowIndex ? { ...item, ...patch } : item))
  }

  function removeEnv(rowIndex: number) {
    setField('env', form.env.filter((_, i) => i !== rowIndex))
  }

  async function handleDeploy() {
    setError('')
    const payload = tab === 'form' ? buildYAML(form) : yaml
    if (!payload.trim()) { setError('YAML is required.'); return }
    try {
      await deploy(payload.trim())
      onDeployed()
      onClose()
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <DataPage.Group surface="bordered" className="mb-4">
      <div className="p-4">
        <div className="mb-4 flex items-center justify-between">
          <span className="text-sm font-semibold">Deploy ModelService</span>
          <div className="flex items-center gap-2">
            <div className="flex overflow-hidden rounded border border-border">
              <Button type="button" size="sm" variant={tab === 'form' ? 'default' : 'ghost'}
                onClick={() => { if (tab !== 'form') setTab('form') }}
                className="h-7 rounded-none border-0 px-3 text-xs">
                Form
              </Button>
              <Button type="button" size="sm" variant={tab === 'yaml' ? 'default' : 'ghost'}
                onClick={() => { setYaml(buildYAML(form)); setTab('yaml') }}
                className="h-7 rounded-none border-0 px-3 text-xs">
                YAML
              </Button>
            </div>
            <Button type="button" variant="ghost" size="sm" onClick={onClose} className="h-7 text-xs">
              ✕ Cancel
            </Button>
          </div>
        </div>

        {tab === 'form' ? (
          <div className="space-y-4">
            <DataBodyTemplate.Field label="Service Name">
              <Input value={form.name} onChange={e => setField('name', e.target.value)} placeholder="my-model" />
            </DataBodyTemplate.Field>

            <DataBodyTemplate.Group layout="stacked" variant="bordered" title="Model Source">
              <div className="grid grid-cols-2 gap-3">
                <DataBodyTemplate.Field label="Pipeline">
                  <Select value={form.pipeline} onValueChange={v => setField('pipeline', v ?? '')}>
                    <SelectTrigger size="sm"><SelectValue placeholder="— select pipeline —" /></SelectTrigger>
                    <SelectContent>
                      {pipelines.map(p => <SelectItem key={p} value={p}>{p}</SelectItem>)}
                    </SelectContent>
                  </Select>
                </DataBodyTemplate.Field>
                <DataBodyTemplate.Field label="Run">
                  <Select value={form.run} onValueChange={v => setField('run', v ?? '')} disabled={!form.pipeline}>
                    <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
                    <SelectContent>
                      <SelectItem value="latest">latest</SelectItem>
                      {pipelineRuns.map(r => (
                        <SelectItem key={r.id} value={r.id}>
                          {r.id.slice(0, 20)}… {r.started_at ? new Date(r.started_at).toLocaleDateString() : ''}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </DataBodyTemplate.Field>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <DataBodyTemplate.Field label="Step">
                  <Select value={form.step} onValueChange={v => setField('step', v ?? '')} disabled={steps.length === 0}>
                    <SelectTrigger size="sm"><SelectValue placeholder="— select step —" /></SelectTrigger>
                    <SelectContent>
                      {steps.map(s => <SelectItem key={s} value={s}>{s}</SelectItem>)}
                    </SelectContent>
                  </Select>
                </DataBodyTemplate.Field>
                <DataBodyTemplate.Field label="Artifact">
                  <Select value={form.artifact} onValueChange={v => setField('artifact', v ?? '')} disabled={artifactNames.length === 0}>
                    <SelectTrigger size="sm"><SelectValue placeholder="— select artifact —" /></SelectTrigger>
                    <SelectContent>
                      {artifactNames.map(a => <SelectItem key={a} value={a}>{a}</SelectItem>)}
                    </SelectContent>
                  </Select>
                </DataBodyTemplate.Field>
              </div>
            </DataBodyTemplate.Group>

            <DataBodyTemplate.Group layout="stacked" variant="bordered" title="Runtime">
              <div className="flex flex-wrap gap-1.5">
                {Object.entries(RUNTIME_TEMPLATES).map(([key, tpl]) => (
                  <button
                    key={key}
                    type="button"
                    title={tpl.description}
                    onClick={() => setForm(prev => ({
                      ...prev,
                      templateKey: key,
                      runtimeMode: tpl.runtimeMode,
                      image: tpl.image,
                      command: tpl.command,
                      port: tpl.port,
                      healthPath: tpl.healthPath,
                    }))}
                    className={`rounded-full border px-2.5 py-1 text-xs font-medium transition-colors ${
                      form.templateKey === key
                        ? 'border-primary bg-primary/10 text-primary'
                        : 'border-border text-muted-foreground hover:border-foreground/30 hover:text-foreground'
                    }`}
                  >
                    {tpl.label}
                  </button>
                ))}
              </div>

              <div className="grid grid-cols-3 gap-3">
                <DataBodyTemplate.Field label="Mode">
                  <Select value={form.runtimeMode} onValueChange={v => setField('runtimeMode', v ?? '')}>
                    <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
                    <SelectContent>
                      <SelectItem value="local">local</SelectItem>
                      <SelectItem value="k8s">k8s</SelectItem>
                    </SelectContent>
                  </Select>
                </DataBodyTemplate.Field>
                <DataBodyTemplate.Field label="Port">
                  <Input value={form.port} onChange={e => setField('port', e.target.value)} placeholder="8000" />
                </DataBodyTemplate.Field>
                <DataBodyTemplate.Field label="Health Path">
                  <Input value={form.healthPath} onChange={e => setField('healthPath', e.target.value)} placeholder="/" />
                </DataBodyTemplate.Field>
              </div>

              {form.runtimeMode === 'local' && (
                <DataBodyTemplate.Field label="Worker" description="Deploy to a specific worker node. Leave blank to auto-assign.">
                  <Select value={form.worker} onValueChange={v => setField('worker', v ?? '')}>
                    <SelectTrigger size="sm"><SelectValue placeholder="— auto assign —" /></SelectTrigger>
                    <SelectContent>
                      <SelectItem value="">auto assign</SelectItem>
                      {servingWorkers.map(w => (
                        <SelectItem key={w.id} value={w.hostname}>
                          {w.hostname}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </DataBodyTemplate.Field>
              )}

              {form.runtimeMode === 'k8s' && (
                <>
                  <DataBodyTemplate.Field label="Container Image">
                    <Input value={form.image} onChange={e => setField('image', e.target.value)} placeholder="registry/image:tag" />
                  </DataBodyTemplate.Field>
                  <div className="grid grid-cols-3 gap-3">
                    <DataBodyTemplate.Field label="Namespace">
                      <Input value={form.k8sNamespace} onChange={e => setField('k8sNamespace', e.target.value)} placeholder="default" />
                    </DataBodyTemplate.Field>
                    <DataBodyTemplate.Field label="Replicas">
                      <Input type="number" min="1" value={form.k8sReplicas} onChange={e => setField('k8sReplicas', e.target.value)} />
                    </DataBodyTemplate.Field>
                    <DataBodyTemplate.Field label="Image Pull Policy">
                      <Select value={form.k8sImagePullPolicy} onValueChange={v => setField('k8sImagePullPolicy', v ?? '')}>
                        <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
                        <SelectContent>
                          <SelectItem value="Always">Always</SelectItem>
                          <SelectItem value="IfNotPresent">IfNotPresent</SelectItem>
                          <SelectItem value="Never">Never</SelectItem>
                        </SelectContent>
                      </Select>
                    </DataBodyTemplate.Field>
                  </div>
                  <div className="grid grid-cols-3 gap-3">
                    <DataBodyTemplate.Field label="CPU"><Input value={form.k8sCPU} onChange={e => setField('k8sCPU', e.target.value)} placeholder="2" /></DataBodyTemplate.Field>
                    <DataBodyTemplate.Field label="Memory"><Input value={form.k8sMemory} onChange={e => setField('k8sMemory', e.target.value)} placeholder="4Gi" /></DataBodyTemplate.Field>
                    <DataBodyTemplate.Field label="GPU"><Input value={form.k8sGPU} onChange={e => setField('k8sGPU', e.target.value)} placeholder="1" /></DataBodyTemplate.Field>
                  </div>
                </>
              )}

              <DataBodyTemplate.Field label="Command" description="One argument per line. $PIPER_MODEL_DIR points to the artifact directory.">
                <YamlMirror
                  className="bg-background"
                  rows={4}
                  value={form.command}
                  onChange={e => setField('command', e.target.value)}
                />
              </DataBodyTemplate.Field>

              <EnvVarEditor
                items={form.env}
                onAdd={addEnv}
                onRemove={removeEnv}
                onUpdate={updateEnv}
              />
            </DataBodyTemplate.Group>
          </div>
        ) : (
          <YamlMirror
            className="bg-background"
            rows={20}
            value={yaml}
            onChange={e => setYaml(e.target.value)}
          />
        )}

        {error && <p className="mt-3 text-sm text-destructive">{error}</p>}

        <div className="mt-4 flex justify-end gap-2">
          <Button variant="outline" size="sm" onClick={onClose}>Cancel</Button>
          <Button size="sm" onClick={() => void handleDeploy()} disabled={deploying}>
            {deploying ? 'Deploying…' : 'Deploy'}
          </Button>
        </div>
      </div>
    </DataPage.Group>
  )
}
