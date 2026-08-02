// serving feature — Deploy form component
import { useEffect, useState } from 'react'
import {
  DataBodyTemplate,
  Select, SelectTrigger, SelectContent, SelectItem, SelectValue,
  Tabs, TabsList, TabsTrigger,
} from '@loykin/designkit'
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm, useWatch } from 'react-hook-form'
import { z } from 'zod'
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

const envVarSchema = z.object({
  name: z.string(),
  value: z.string(),
  source: z.enum(['value', 'credential']),
  credentialName: z.string(),
  credentialKey: z.string(),
})

const deployFormSchema = z.object({
  name: z.string().trim().min(1, 'Service name is required.'),
  env: z.array(envVarSchema),
  pipeline: z.string().min(1, 'Pipeline is required.'),
  run: z.string().min(1),
  step: z.string().min(1, 'Step is required.'),
  artifact: z.string().min(1, 'Artifact is required.'),
  templateKey: z.string(),
  runtimeMode: z.string(),
  image: z.string(),
  command: z.string().trim().min(1, 'Command is required.'),
  port: z.string().regex(/^\d+$/, 'Port must be a number.').refine(value => {
    const port = Number(value)
    return port > 0 && port <= 65535
  }, 'Port must be between 1 and 65535.'),
  healthPath: z.string().trim().min(1, 'Health path is required.'),
  worker: z.string(),
  k8sNamespace: z.string(),
  k8sReplicas: z.string(),
  k8sCPU: z.string(),
  k8sMemory: z.string(),
  k8sGPU: z.string(),
  k8sImagePullPolicy: z.string(),
}).superRefine((values, context) => {
  if (values.runtimeMode !== 'k8s') return
  if (!values.image.trim()) {
    context.addIssue({ code: 'custom', path: ['image'], message: 'Container image is required for Kubernetes.' })
  }
  if (!/^[1-9]\d*$/.test(values.k8sReplicas)) {
    context.addIssue({ code: 'custom', path: ['k8sReplicas'], message: 'Replicas must be at least 1.' })
  }
})

export function DeployForm({ onClose, onDeployed }: DeployFormProps) {
  const projectId = useProjectId()
  const { data: allRuns = [] } = useRuns({ status: 'success' })
  const { data: servingWorkers = [] } = useServingWorkers()
  const { mutateAsync: deploy, isPending: deploying } = useCreateService()

  const [tab, setTab] = useState<'form' | 'yaml'>('form')
  const [yaml, setYaml] = useState(() => buildYAML(DEFAULT_FORM))
  const [error, setError] = useState('')
  const [artifacts, setArtifacts] = useState<StepArtifacts[]>([])
  const {
    control,
    setValue,
    handleSubmit,
    formState: { errors },
  } = useForm<FormState>({
    resolver: zodResolver(deployFormSchema),
    defaultValues: DEFAULT_FORM,
  })
  const form = useWatch({ control, defaultValue: DEFAULT_FORM }) as FormState

  const pipelines = Array.from(new Set(allRuns.map(r => r.pipeline_name))).sort()
  const pipelineRuns = allRuns.filter(r => r.pipeline_name === form.pipeline)
  const selectedRunID = form.pipeline
    ? (form.run === 'latest' ? pipelineRuns[0]?.id : form.run)
    : undefined
  const compatibleWorkers = servingWorkers.filter(worker =>
    form.runtimeMode === 'k8s'
      ? worker.infrastructure === 'k8s'
      : worker.infrastructure !== 'k8s',
  )
  const hasCompatibleWorkers = compatibleWorkers.length > 0
  const localInfraTypes = [...new Set(compatibleWorkers.map(w => w.infrastructure))]
  const ambiguousLocalInfra = form.runtimeMode === 'local' && localInfraTypes.length > 1 && !form.worker

  useEffect(() => {
    let canceled = false
    if (!selectedRunID) {
      queueMicrotask(() => {
        if (!canceled) setArtifacts([])
      })
      return () => { canceled = true }
    }
    void listArtifacts(projectId, selectedRunID)
      .then(result => {
        if (!canceled) setArtifacts(result)
      })
      .catch(() => {
        if (!canceled) setArtifacts([])
      })
    return () => { canceled = true }
  }, [projectId, selectedRunID])

  const steps = artifacts.map(sa => sa.step)
  const artifactNames = artifacts.find(sa => sa.step === form.step)?.artifacts.map(a => a.name) ?? []

  function setField<K extends keyof FormState>(key: K, value: FormState[K]) {
    const next = { ...form, [key]: value }
    if (key === 'pipeline') { next.run = 'latest'; next.step = ''; next.artifact = '' }
    if (key === 'run') { next.step = ''; next.artifact = '' }
    if (key === 'step') { next.artifact = '' }
    if (key === 'command' || key === 'port' || key === 'healthPath') next.templateKey = 'custom'
    for (const [field, fieldValue] of Object.entries(next) as [keyof FormState, FormState[keyof FormState]][]) {
      setValue(field, fieldValue, { shouldDirty: true, shouldValidate: false })
    }
    setYaml(buildYAML(next))
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

  async function deployPayload(payload: string) {
    setError('')
    if (!payload.trim()) { setError('YAML is required.'); return }
    try {
      await deploy(payload.trim())
      onDeployed()
      onClose()
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  async function handleDeploy(values: FormState) {
    if (!hasCompatibleWorkers) {
      setError(`No ${values.runtimeMode === 'k8s' ? 'Kubernetes' : 'local'} serving worker is connected.`)
      return
    }
    if (ambiguousLocalInfra) {
      setError(`Multiple worker infrastructure types are registered (${localInfraTypes.join(', ')}) — choose a Worker before deploying.`)
      return
    }
    await deployPayload(buildYAML(values))
  }

  function handleYamlSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault()
    void deployPayload(yaml)
  }

  return (
    <DataBodyTemplate.Group
      layout="stacked"
      variant="bordered"
      title="Deploy ModelService"
      description="Deploy a pipeline artifact as a managed model serving endpoint."
    >
      <form
        onSubmit={tab === 'form' ? handleSubmit(handleDeploy) : handleYamlSubmit}
        className="space-y-4"
        noValidate
      >
        <div className="flex items-center justify-between">
          <Tabs
            value={tab}
            onValueChange={value => {
              const nextTab = value as 'form' | 'yaml'
              if (nextTab === 'yaml') setYaml(buildYAML(form))
              setTab(nextTab)
            }}
          >
            <TabsList>
              <TabsTrigger value="form">Form</TabsTrigger>
              <TabsTrigger value="yaml">YAML</TabsTrigger>
            </TabsList>
          </Tabs>
          <Button type="button" variant="ghost" size="sm" onClick={onClose}>Cancel</Button>
        </div>

        {tab === 'form' ? (
          <div className="space-y-4">
            {!hasCompatibleWorkers && (
              <p className="rounded border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
                No compatible serving worker is connected for the selected runtime.
              </p>
            )}
            <DataBodyTemplate.Field label="Service Name">
              <Input
                value={form.name}
                onChange={e => setField('name', e.target.value)}
                placeholder="my-model"
                aria-invalid={!!errors.name}
              />
              {errors.name && <p className="mt-1 text-xs text-destructive">{errors.name.message}</p>}
            </DataBodyTemplate.Field>

            <DataBodyTemplate.Group layout="stacked" variant="bordered" title="Model Source">
              <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
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
              <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
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
                  <Button
                    key={key}
                    type="button"
                    size="sm"
                    variant={form.templateKey === key ? 'default' : 'outline'}
                    title={tpl.description}
                    onClick={() => {
                      const next = {
                      ...form,
                      templateKey: key,
                      runtimeMode: tpl.runtimeMode,
                      image: tpl.image,
                      command: tpl.command,
                      port: tpl.port,
                      healthPath: tpl.healthPath,
                      }
                      for (const [field, fieldValue] of Object.entries(next) as [keyof FormState, FormState[keyof FormState]][]) {
                        setValue(field, fieldValue, { shouldDirty: true })
                      }
                      setYaml(buildYAML(next))
                    }}
                    className="rounded-full"
                  >
                    {tpl.label}
                  </Button>
                ))}
              </div>

              {!hasCompatibleWorkers && (
                <p className="rounded border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
                  No {form.runtimeMode === 'k8s' ? 'Kubernetes' : 'local'} serving worker is connected. Deploying would fail immediately — register one first, or switch Mode.
                </p>
              )}
              {ambiguousLocalInfra && (
                <p className="rounded border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
                  {localInfraTypes.length} worker infrastructure types are registered ({localInfraTypes.join(', ')}) — choose a Worker so this service always runs where you expect.
                </p>
              )}
              <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
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
                  <Input
                    value={form.port}
                    onChange={e => setField('port', e.target.value)}
                    placeholder="8000"
                    aria-invalid={!!errors.port}
                  />
                  {errors.port && <p className="mt-1 text-xs text-destructive">{errors.port.message}</p>}
                </DataBodyTemplate.Field>
                <DataBodyTemplate.Field label="Health Path">
                  <Input value={form.healthPath} onChange={e => setField('healthPath', e.target.value)} placeholder="/" />
                </DataBodyTemplate.Field>
              </div>

              {form.runtimeMode === 'local' && (
                <DataBodyTemplate.Field
                  label="Worker"
                  description={
                    localInfraTypes.length > 1
                      ? 'Multiple infrastructure types are registered — pick the worker to run on.'
                      : 'Optional. Leave unassigned to load-balance across matching workers.'
                  }
                >
                  <Select
                    value={form.worker || (localInfraTypes.length <= 1 ? '__auto__' : '')}
                    onValueChange={v => setField('worker', v === '__auto__' ? '' : (v ?? ''))}
                  >
                    <SelectTrigger size="sm"><SelectValue placeholder="— select worker —" /></SelectTrigger>
                    <SelectContent>
                      {localInfraTypes.length <= 1 && <SelectItem value="__auto__">auto assign</SelectItem>}
                      {compatibleWorkers.map(w => (
                        <SelectItem key={w.id} value={w.id}>
                          {w.hostname || w.id}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </DataBodyTemplate.Field>
              )}

              {form.runtimeMode === 'k8s' && (
                <>
                  <DataBodyTemplate.Field label="Container Image">
                    <Input
                      value={form.image}
                      onChange={e => setField('image', e.target.value)}
                      placeholder="registry/image:tag"
                      aria-invalid={!!errors.image}
                    />
                    {errors.image && <p className="mt-1 text-xs text-destructive">{errors.image.message}</p>}
                  </DataBodyTemplate.Field>
                  <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
                    <DataBodyTemplate.Field label="Namespace">
                      <Input value={form.k8sNamespace} onChange={e => setField('k8sNamespace', e.target.value)} placeholder="default" />
                    </DataBodyTemplate.Field>
                    <DataBodyTemplate.Field label="Replicas">
                      <Input
                        type="number"
                        min="1"
                        value={form.k8sReplicas}
                        onChange={e => setField('k8sReplicas', e.target.value)}
                        aria-invalid={!!errors.k8sReplicas}
                      />
                      {errors.k8sReplicas && <p className="mt-1 text-xs text-destructive">{errors.k8sReplicas.message}</p>}
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
                  <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
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

        {error && <p className="text-sm text-destructive" role="alert">{error}</p>}

        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" size="sm" onClick={onClose}>Cancel</Button>
          <Button
            type="submit"
            size="sm"
            disabled={deploying || (tab === 'form' && (!hasCompatibleWorkers || ambiguousLocalInfra))}
          >
            {deploying ? 'Deploying…' : 'Deploy'}
          </Button>
        </div>
      </form>
    </DataBodyTemplate.Group>
  )
}
