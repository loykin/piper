// serving feature — Deploy form component
import { useEffect, useState } from 'react'
import {
  DataBodyTemplate,
  Select, SelectTrigger, SelectContent, SelectItem, SelectValue,
  Tabs, TabsList, TabsTrigger,
} from '@loykin/designkit'
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm, useWatch, type FieldErrors, type UseFormSetValue } from 'react-hook-form'
import { z } from 'zod'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { YamlMirror } from '@/components/ui/yaml-mirror'
import { EnvVarEditor } from '@/shared/components/EnvVarEditor'
import { emptyEnvVarDraft, type EnvVarDraft } from '@/shared/env'
import { useRuns } from '@/features/runs/hooks'
import type { Run } from '@/features/runs/api'
import { listArtifacts, type StepArtifacts } from '@/features/runs/api'
import { useCreateService, useServingWorkers } from '../hooks'
import type { ServingWorkerInfo } from '../types'
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
  k8sImage: z.string(),
  dockerImage: z.string(),
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
  if (!values.k8sImage.trim()) {
    context.addIssue({ code: 'custom', path: ['k8sImage'], message: 'Container image is required for Kubernetes.' })
  }
  if (!/^[1-9]\d*$/.test(values.k8sReplicas)) {
    context.addIssue({ code: 'custom', path: ['k8sReplicas'], message: 'Replicas must be at least 1.' })
  }
})

// ─── ServiceSection ─────────────────────────────────────────────────────────

interface ServiceSectionProps {
  name: string
  error?: string
  onChange: (value: string) => void
}

function ServiceSection({ name, error, onChange }: ServiceSectionProps) {
  return (
    <DataBodyTemplate.Group layout="stacked" title="Service">
      <div className="space-y-1.5">
        <Label className="text-xs">Service Name</Label>
        <Input
          className="h-8 text-sm"
          value={name}
          onChange={e => onChange(e.target.value)}
          placeholder="my-model"
          aria-invalid={!!error}
        />
        {error && <p className="text-xs text-destructive">{error}</p>}
      </div>
    </DataBodyTemplate.Group>
  )
}

// ─── ModelSourceSection ─────────────────────────────────────────────────────

interface ModelSourceSectionProps {
  form: FormState
  pipelines: string[]
  pipelineRuns: Run[]
  steps: string[]
  artifactNames: string[]
  setField: <K extends keyof FormState>(key: K, value: FormState[K]) => void
}

function ModelSourceSection({ form, pipelines, pipelineRuns, steps, artifactNames, setField }: ModelSourceSectionProps) {
  return (
    <DataBodyTemplate.Group layout="stacked" title="Model Source">
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <div className="space-y-1.5">
          <Label className="text-xs">Pipeline</Label>
          <Select value={form.pipeline} onValueChange={v => setField('pipeline', v ?? '')}>
            <SelectTrigger size="sm" className="h-8 text-sm"><SelectValue placeholder="— select pipeline —" /></SelectTrigger>
            <SelectContent>
              {pipelines.map(p => <SelectItem key={p} value={p}>{p}</SelectItem>)}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-1.5">
          <Label className="text-xs">Run</Label>
          <Select value={form.run} onValueChange={v => setField('run', v ?? '')} disabled={!form.pipeline}>
            <SelectTrigger size="sm" className="h-8 text-sm"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="latest">latest</SelectItem>
              {pipelineRuns.map(r => (
                <SelectItem key={r.id} value={r.id}>
                  {r.id.slice(0, 20)}… {r.started_at ? new Date(r.started_at).toLocaleDateString() : ''}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <div className="space-y-1.5">
          <Label className="text-xs">Step</Label>
          <Select value={form.step} onValueChange={v => setField('step', v ?? '')} disabled={steps.length === 0}>
            <SelectTrigger size="sm" className="h-8 text-sm"><SelectValue placeholder="— select step —" /></SelectTrigger>
            <SelectContent>
              {steps.map(s => <SelectItem key={s} value={s}>{s}</SelectItem>)}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-1.5">
          <Label className="text-xs">Artifact</Label>
          <Select value={form.artifact} onValueChange={v => setField('artifact', v ?? '')} disabled={artifactNames.length === 0}>
            <SelectTrigger size="sm" className="h-8 text-sm"><SelectValue placeholder="— select artifact —" /></SelectTrigger>
            <SelectContent>
              {artifactNames.map(a => <SelectItem key={a} value={a}>{a}</SelectItem>)}
            </SelectContent>
          </Select>
        </div>
      </div>
    </DataBodyTemplate.Group>
  )
}

// ─── RuntimeSection ─────────────────────────────────────────────────────────

interface RuntimeSectionProps {
  form: FormState
  errors: FieldErrors<FormState>
  setField: <K extends keyof FormState>(key: K, value: FormState[K]) => void
  setValue: UseFormSetValue<FormState>
  setYaml: (yaml: string) => void
  hasCompatibleWorkers: boolean
  ambiguousLocalInfra: boolean
  workerRequired: boolean
  localInfraTypes: string[]
  compatibleWorkers: ServingWorkerInfo[]
  isDockerLocal: boolean
}

function RuntimeSection({
  form, errors, setField, setValue, setYaml,
  hasCompatibleWorkers, ambiguousLocalInfra, workerRequired, localInfraTypes, compatibleWorkers, isDockerLocal,
}: RuntimeSectionProps) {
  return (
    <DataBodyTemplate.Group layout="stacked" title="Runtime">
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
                k8sImage: tpl.image,
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
        <div className="space-y-1.5">
          <Label className="text-xs">Mode</Label>
          <Select value={form.runtimeMode} onValueChange={v => setField('runtimeMode', v ?? '')}>
            <SelectTrigger size="sm" className="h-8 text-sm"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="local">local</SelectItem>
              <SelectItem value="k8s">k8s</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-1.5">
          <Label className="text-xs">Port</Label>
          <Input
            className="h-8 text-sm"
            value={form.port}
            onChange={e => setField('port', e.target.value)}
            placeholder="8000"
            aria-invalid={!!errors.port}
          />
          {errors.port && <p className="text-xs text-destructive">{errors.port.message}</p>}
        </div>
        <div className="space-y-1.5">
          <Label className="text-xs">Health Path</Label>
          <Input className="h-8 text-sm" value={form.healthPath} onChange={e => setField('healthPath', e.target.value)} placeholder="/" />
        </div>
      </div>

      {form.runtimeMode === 'local' && (
        <div className="space-y-1.5">
          <Label className="text-xs">Worker</Label>
          <p className="text-xs text-muted-foreground">
            {localInfraTypes.length > 1
              ? 'Multiple infrastructure types are registered — pick the worker to run on.'
              : workerRequired
                ? 'Multiple workers are registered — pick the worker to run on. Separately managed workers of the same type are never chosen automatically.'
                : 'Optional. Only one compatible worker is registered, so it will be used automatically.'}
          </p>
          <Select
            value={form.worker}
            onValueChange={v => setField('worker', v ?? '')}
          >
            <SelectTrigger size="sm" className="h-8 text-sm" aria-invalid={workerRequired && !form.worker}><SelectValue placeholder="— select worker —" /></SelectTrigger>
            <SelectContent>
              {compatibleWorkers.map(w => (
                <SelectItem key={w.id} value={w.id}>
                  <span className={`mr-1.5 rounded px-1 py-0.5 text-[10px] font-medium ${
                    w.infrastructure === 'docker' ? 'bg-cyan-500/15 text-cyan-400' : 'bg-orange-500/15 text-orange-400'
                  }`}>
                    {w.infrastructure === 'docker' ? 'Docker' : 'BM'}
                  </span>
                  {w.hostname || w.id}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {workerRequired && !form.worker && <p className="text-xs text-destructive">Worker is required.</p>}
        </div>
      )}

      {isDockerLocal && (
        <div className="space-y-1.5">
          <Label className="text-xs">Container Image</Label>
          <Input
            className="h-8 text-sm"
            value={form.dockerImage}
            onChange={e => setField('dockerImage', e.target.value)}
            placeholder="registry/image:tag"
          />
        </div>
      )}

      {form.runtimeMode === 'k8s' && (
        <>
          <div className="space-y-1.5">
            <Label className="text-xs">Worker</Label>
            <p className="text-xs text-muted-foreground">
              {workerRequired
                ? 'Multiple Kubernetes clusters are registered — pick the cluster to deploy to. Separately managed clusters are never chosen automatically.'
                : 'Optional. Only one compatible cluster is registered, so it will be used automatically.'}
            </p>
            <Select
              value={form.worker}
              onValueChange={v => setField('worker', v ?? '')}
            >
              <SelectTrigger size="sm" className="h-8 text-sm" aria-invalid={workerRequired && !form.worker}><SelectValue placeholder="— select worker —" /></SelectTrigger>
              <SelectContent>
                {compatibleWorkers.map(w => (
                  <SelectItem key={w.id} value={w.id}>{w.hostname || w.id}</SelectItem>
                ))}
              </SelectContent>
            </Select>
            {workerRequired && !form.worker && <p className="text-xs text-destructive">Worker is required.</p>}
          </div>
          <div className="space-y-1.5">
            <Label className="text-xs">Container Image</Label>
            <Input
              className="h-8 text-sm"
              value={form.k8sImage}
              onChange={e => setField('k8sImage', e.target.value)}
              placeholder="registry/image:tag"
              aria-invalid={!!errors.k8sImage}
            />
            {errors.k8sImage && <p className="text-xs text-destructive">{errors.k8sImage.message}</p>}
          </div>
          <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
            <div className="space-y-1.5">
              <Label className="text-xs">Namespace</Label>
              <Input className="h-8 text-sm" value={form.k8sNamespace} onChange={e => setField('k8sNamespace', e.target.value)} placeholder="default" />
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs">Replicas</Label>
              <Input
                className="h-8 text-sm"
                type="number"
                min="1"
                value={form.k8sReplicas}
                onChange={e => setField('k8sReplicas', e.target.value)}
                aria-invalid={!!errors.k8sReplicas}
              />
              {errors.k8sReplicas && <p className="text-xs text-destructive">{errors.k8sReplicas.message}</p>}
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs">Image Pull Policy</Label>
              <Select value={form.k8sImagePullPolicy} onValueChange={v => setField('k8sImagePullPolicy', v ?? '')}>
                <SelectTrigger size="sm" className="h-8 text-sm"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="Always">Always</SelectItem>
                  <SelectItem value="IfNotPresent">IfNotPresent</SelectItem>
                  <SelectItem value="Never">Never</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
          <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
            <div className="space-y-1.5">
              <Label className="text-xs">CPU</Label>
              <Input className="h-8 text-sm" value={form.k8sCPU} onChange={e => setField('k8sCPU', e.target.value)} placeholder="2" />
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs">Memory</Label>
              <Input className="h-8 text-sm" value={form.k8sMemory} onChange={e => setField('k8sMemory', e.target.value)} placeholder="4Gi" />
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs">GPU</Label>
              <Input className="h-8 text-sm" value={form.k8sGPU} onChange={e => setField('k8sGPU', e.target.value)} placeholder="1" />
            </div>
          </div>
        </>
      )}

      <div className="space-y-1.5">
        <Label className="text-xs">Command</Label>
        <p className="text-xs text-muted-foreground">One argument per line. $PIPER_MODEL_DIR points to the artifact directory.</p>
        <YamlMirror
          className="bg-background"
          rows={4}
          value={form.command}
          onChange={e => setField('command', e.target.value)}
        />
      </div>
    </DataBodyTemplate.Group>
  )
}

// ─── EnvironmentSection ─────────────────────────────────────────────────────

interface EnvironmentSectionProps {
  items: EnvVarDraft[]
  onAdd: () => void
  onRemove: (rowIndex: number) => void
  onUpdate: (rowIndex: number, patch: Partial<EnvVarDraft>) => void
}

function EnvironmentSection({ items, onAdd, onRemove, onUpdate }: EnvironmentSectionProps) {
  return (
    <DataBodyTemplate.Group layout="stacked" title="Environment">
      <EnvVarEditor items={items} onAdd={onAdd} onRemove={onRemove} onUpdate={onUpdate} />
    </DataBodyTemplate.Group>
  )
}

// ─── DeployForm ─────────────────────────────────────────────────────────────

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
  // Mirrors the router's actual rule (internal/agent/router.go): a worker
  // must be named only when more than one candidate could otherwise match.
  // A single compatible worker is not ambiguous, so the backend resolves it
  // without an explicit ID — the form shouldn't demand one either.
  const workerRequired = compatibleWorkers.length > 1
  const selectedWorkerInfra = form.worker
    ? compatibleWorkers.find(w => w.id === form.worker)?.infrastructure
    : (localInfraTypes.length === 1 ? localInfraTypes[0] : undefined)
  const isDockerLocal = form.runtimeMode === 'local' && selectedWorkerInfra === 'docker'

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
    if (workerRequired && !values.worker.trim()) {
      setError('Multiple compatible workers are registered — choose a Worker before deploying.')
      return
    }
    if (isDockerLocal && !values.dockerImage.trim()) {
      setError('Container image is required for the selected Docker worker.')
      return
    }
    await deployPayload(buildYAML(values))
  }

  function handleYamlSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault()
    void deployPayload(yaml)
  }

  return (
    <>
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

      <form
        className="contents"
        onSubmit={tab === 'form' ? handleSubmit(handleDeploy) : handleYamlSubmit}
        noValidate
      >
        {tab === 'form' ? (
          <>
            {!hasCompatibleWorkers && (
              <p className="rounded border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
                No compatible serving worker is connected for the selected runtime.
              </p>
            )}

            <ServiceSection
              name={form.name}
              error={errors.name?.message}
              onChange={value => setField('name', value)}
            />

            <ModelSourceSection
              form={form}
              pipelines={pipelines}
              pipelineRuns={pipelineRuns}
              steps={steps}
              artifactNames={artifactNames}
              setField={setField}
            />

            <RuntimeSection
              form={form}
              errors={errors}
              setField={setField}
              setValue={setValue}
              setYaml={setYaml}
              hasCompatibleWorkers={hasCompatibleWorkers}
              ambiguousLocalInfra={ambiguousLocalInfra}
              workerRequired={workerRequired}
              localInfraTypes={localInfraTypes}
              compatibleWorkers={compatibleWorkers}
              isDockerLocal={isDockerLocal}
            />

            <EnvironmentSection
              items={form.env}
              onAdd={addEnv}
              onRemove={removeEnv}
              onUpdate={updateEnv}
            />
          </>
        ) : (
          <DataBodyTemplate.Group layout="stacked" title="YAML">
            <YamlMirror
              className="bg-background"
              rows={20}
              value={yaml}
              onChange={e => setYaml(e.target.value)}
            />
          </DataBodyTemplate.Group>
        )}

        {error && <p className="text-sm text-destructive" role="alert">{error}</p>}

        <div className="flex justify-end gap-2 border-t border-border pt-(--designkit-panel-gap)">
          <Button type="button" variant="outline" size="sm" onClick={onClose}>Cancel</Button>
          <Button
            type="submit"
            size="sm"
            disabled={deploying || (tab === 'form' && (!hasCompatibleWorkers || ambiguousLocalInfra || (workerRequired && !form.worker)))}
          >
            {deploying ? 'Deploying…' : 'Deploy'}
          </Button>
        </div>
      </form>
    </>
  )
}
