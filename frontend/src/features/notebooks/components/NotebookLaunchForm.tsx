// notebooks feature — launch form (Form and YAML tabs) for the installation's runtime
import { useState, type ReactNode } from 'react'
import { zodResolver } from '@hookform/resolvers/zod'
import {
  DataBodyTemplate, FormField,
  Select, SelectTrigger, SelectContent, SelectItem, SelectValue,
  Tabs, TabsList, TabsTrigger,
} from '@loykin/designkit'
import { Controller, useFieldArray, useForm, type Control, type FieldErrors, type Path, type UseFormRegister } from 'react-hook-form'
import { z } from 'zod'
import { Input } from '@/components/ui/input'
import { ShellMirror } from '@/components/ui/shell-mirror'
import { YamlMirror } from '@/components/ui/yaml-mirror'
import { EnvVarEditor } from '@/features/credentials/components/EnvVarEditor'
import { emptyEnvVarDraft, type EnvVarDraft } from '@/shared/env'
import type { NotebookVolume } from '../types'
import {
  buildK8sYAML, buildWorkerYAMLWithBackend,
  DEFAULT_K8S, DEFAULT_WORKER,
  type K8sFormState, type WorkerFormState,
} from '../editor'
import { FormSubmitBar } from '@/shared/components/FormSubmitBar'

export type NotebookRuntime = 'k8s' | 'docker' | 'baremetal'

interface NotebookLaunchFormProps {
  runtime: NotebookRuntime
  releasedVolumes: NotebookVolume[]
  preselectedVolume?: string
  onSubmit: (yaml: string, volumeId?: string) => void
  submitting: boolean
  error?: string
  onCancel: () => void
}

/** The launch form body; the route page owns the page shell around it. */
export function NotebookLaunchForm(props: NotebookLaunchFormProps) {
  // Each runtime has its own field set and validation, so each gets its own
  // form instance; remounting on a runtime change resets it cleanly.
  return props.runtime === 'k8s'
    ? <K8sLaunchForm key="k8s" {...props} />
    : <WorkerLaunchForm key={props.runtime} {...props} />
}

// ─── Shared schema pieces ───────────────────────────────────────────────────

const envVarSchema = z.object({
  name: z.string(),
  value: z.string(),
  source: z.enum(['value', 'credential']),
  credentialName: z.string(),
  credentialKey: z.string(),
})

const required = (label: string) => z.string().trim().min(1, `${label} is required.`)

// ─── Kubernetes ─────────────────────────────────────────────────────────────

const k8sSchema = z.object({
  name: required('Server Name'),
  image: required('Image'),
  namespace: required('Namespace'),
  storageSize: required('Storage Size'),
  cpu: z.string(),
  memory: z.string(),
  gpu: z.string(),
  prepareBackend: z.literal('k8s'),
  prepare: z.string(),
  env: z.array(envVarSchema),
}) satisfies z.ZodType<K8sFormState>

function K8sLaunchForm({ releasedVolumes, preselectedVolume = '', onSubmit, submitting, error, onCancel }: NotebookLaunchFormProps) {
  const [volumeId, setVolumeId] = useState(preselectedVolume)
  const form = useForm<K8sFormState>({ resolver: zodResolver(k8sSchema), defaultValues: DEFAULT_K8S })
  const { register, control, formState: { errors } } = form
  const env = useEnvVarFields(control, 'env')

  return (
    <LaunchFrame
      buildYaml={() => buildK8sYAML(form.getValues())}
      submitForm={form.handleSubmit(values => onSubmit(buildK8sYAML(values), volumeId || undefined))}
      onSubmitYaml={yaml => onSubmit(yaml, volumeId || undefined)}
      submitting={submitting}
      error={error}
      onCancel={onCancel}
    >
      <DataBodyTemplate.Group layout="stacked" title="Server">
        <TextField id="k8s-name" label="Server Name" field="name" register={register} errors={errors} placeholder="my-notebook" autoFocus />
        <VolumeField volumeId={volumeId} releasedVolumes={releasedVolumes} onChange={setVolumeId} />
        <TextField id="k8s-image" label="Image" field="image" register={register} errors={errors} helperText="Required container image for the notebook server." />
        <TextField id="k8s-namespace" label="Namespace" field="namespace" register={register} errors={errors} helperText="Required. Kubernetes namespace where the notebook and its volume will be created. Defaults to notebooks." />
        <TextField id="k8s-storage-size" label="Storage Size" field="storageSize" register={register} errors={errors} helperText="Required PVC size. Defaults to 10Gi." />
        <PrepareField id="k8s-prepare" control={control} placeholder={'pip install -r requirements.txt\npython /work/preflight.py'} />
        <EnvVarEditor items={env.items} onAdd={env.add} onRemove={env.remove} onUpdate={env.update} />
      </DataBodyTemplate.Group>
      <DataBodyTemplate.Group layout="stacked" title="Resources" description="Optional CPU, memory, and GPU requests/limits.">
        <div className="grid grid-cols-3 gap-3">
          <TextField id="k8s-cpu" label="CPU" field="cpu" register={register} errors={errors} placeholder="2" />
          <TextField id="k8s-memory" label="Memory" field="memory" register={register} errors={errors} placeholder="4Gi" />
          <TextField id="k8s-gpu" label="GPU" field="gpu" register={register} errors={errors} placeholder="1" />
        </div>
      </DataBodyTemplate.Group>
    </LaunchFrame>
  )
}

// ─── Docker / bare-metal ────────────────────────────────────────────────────

function workerSchema(runtime: NotebookRuntime) {
  return z.object({
    name: required('Server Name'),
    dockerImage: z.string(),
    env: z.string(),
    gpus: z.string(),
    prepareBackend: z.enum(['process', 'docker']),
    prepare: z.string(),
    envVars: z.array(envVarSchema),
  }).superRefine((values, ctx) => {
    if (runtime === 'docker' && !values.dockerImage.trim()) {
      ctx.addIssue({ code: 'custom', path: ['dockerImage'], message: 'Image is required.' })
    }
  })
}

function WorkerLaunchForm({ runtime, releasedVolumes, preselectedVolume = '', onSubmit, submitting, error, onCancel }: NotebookLaunchFormProps) {
  const backend = runtime === 'docker' ? 'docker' : 'process'
  const [volumeId, setVolumeId] = useState(preselectedVolume)
  const form = useForm<WorkerFormState>({
    resolver: zodResolver(workerSchema(runtime)),
    defaultValues: { ...DEFAULT_WORKER, prepareBackend: backend },
  })
  const { register, control, formState: { errors } } = form
  const env = useEnvVarFields(control, 'envVars')

  return (
    <LaunchFrame
      buildYaml={() => buildWorkerYAMLWithBackend(form.getValues(), backend)}
      submitForm={form.handleSubmit(values => onSubmit(buildWorkerYAMLWithBackend(values, backend), volumeId || undefined))}
      onSubmitYaml={yaml => onSubmit(yaml, volumeId || undefined)}
      submitting={submitting}
      error={error}
      onCancel={onCancel}
    >
      <DataBodyTemplate.Group layout="stacked" title="Server">
        <TextField id="worker-name" label="Server Name" field="name" register={register} errors={errors} placeholder="my-notebook" autoFocus />
        <VolumeField volumeId={volumeId} releasedVolumes={releasedVolumes} onChange={setVolumeId} />
        {runtime === 'docker' ? (
          <TextField id="worker-image" label="Image" field="dockerImage" register={register} errors={errors} helperText="Container image used to run the notebook server." />
        ) : (
          <TextField
            id="worker-env"
            label="Python Environment"
            field="env"
            register={register}
            errors={errors}
            helperText="venv path (e.g. /project/venv) or conda env (e.g. conda:ml-env). Leave blank to auto-create a .venv."
            placeholder="/home/user/project/venv"
          />
        )}
        <TextField id="worker-gpus" label="GPUs" field="gpus" register={register} errors={errors} helperText="Device IDs: 0 · 0,1 · all · leave blank for no GPU" placeholder="0" />
        <PrepareField id="worker-prepare" control={control} placeholder={'uv pip install jupyterlab ipykernel\npython -m ipykernel install --sys-prefix'} />
        <EnvVarEditor items={env.items} onAdd={env.add} onRemove={env.remove} onUpdate={env.update} />
      </DataBodyTemplate.Group>
    </LaunchFrame>
  )
}

// ─── Building blocks ────────────────────────────────────────────────────────

/** Form/YAML tabs plus the action row. YAML is seeded from the form on switch. */
function LaunchFrame({
  buildYaml, submitForm, onSubmitYaml, submitting, error, onCancel, children,
}: {
  buildYaml: () => string
  submitForm: () => void
  onSubmitYaml: (yaml: string) => void
  submitting: boolean
  error?: string
  onCancel: () => void
  children: ReactNode
}) {
  const [tab, setTab] = useState('form')
  const [yaml, setYaml] = useState('')

  function changeTab(next: string) {
    if (tab === 'form' && next === 'yaml') setYaml(buildYaml())
    setTab(next)
  }

  return (
    <>
      <Tabs value={tab} onValueChange={value => changeTab(value ?? 'form')}>
        <TabsList>
          <TabsTrigger value="form">Form</TabsTrigger>
          <TabsTrigger value="yaml">YAML</TabsTrigger>
        </TabsList>
      </Tabs>
      <form
        className="contents"
        noValidate
        onSubmit={e => {
          e.preventDefault()
          if (tab === 'form') submitForm()
          else if (yaml.trim()) onSubmitYaml(yaml.trim())
        }}
      >
        {tab === 'form' ? children : (
          <DataBodyTemplate.Group layout="stacked" title="YAML">
            <YamlMirror rows={24} value={yaml} onChange={e => setYaml(e.target.value)} />
          </DataBodyTemplate.Group>
        )}
        <FormSubmitBar
          verb="Launch"
          noun="Notebook"
          pending={submitting}
          error={error}
          disabled={(tab === 'yaml' && !yaml.trim())}
          onCancel={onCancel}
        />
      </form>
    </>
  )
}

type FormValues = K8sFormState | WorkerFormState

function TextField<T extends FormValues>({
  id, label, field, register, errors, helperText, placeholder, autoFocus,
}: {
  id: string
  label: string
  field: keyof T & string
  register: UseFormRegister<T>
  errors: FieldErrors<T>
  helperText?: string
  placeholder?: string
  autoFocus?: boolean
}) {
  const message = errors[field]?.message
  return (
    <FormField label={label} htmlFor={id} error={typeof message === 'string' ? message : undefined} helperText={helperText}>
      <Input
        id={id}
        className="h-8 text-sm"
        placeholder={placeholder}
        autoFocus={autoFocus}
        aria-invalid={!!message}
        {...register(field as Path<T>)}
      />
    </FormField>
  )
}

function PrepareField<T extends FormValues>({ id, control, placeholder }: { id: string; control: Control<T>; placeholder: string }) {
  return (
    <FormField label="Prepare Commands" htmlFor={id} helperText="One command per line. Runs before notebook start.">
      <Controller
        name={'prepare' as Path<T>}
        control={control}
        render={({ field }) => (
          <ShellMirror
            value={String(field.value ?? '')}
            onChange={e => field.onChange(e.target.value)}
            minHeight="7rem"
            placeholder={placeholder}
          />
        )}
      />
    </FormField>
  )
}

/** Adapts a react-hook-form field array to EnvVarEditor's items/add/remove/update API. */
function useEnvVarFields<T extends FormValues>(control: Control<T>, name: 'env' | 'envVars') {
  const { fields, append, remove, update } = useFieldArray({ control: control as unknown as Control<FormValues>, name: name as never })
  const items = fields as unknown as (EnvVarDraft & { id: string })[]
  return {
    items: items as EnvVarDraft[],
    add: () => append(emptyEnvVarDraft() as never),
    remove: (index: number) => remove(index),
    update: (index: number, patch: Partial<EnvVarDraft>) => {
      const { id: _id, ...current } = items[index]
      update(index, { ...current, ...patch } as never)
    },
  }
}

function VolumeField({ volumeId, releasedVolumes, onChange }: { volumeId: string; releasedVolumes: NotebookVolume[]; onChange: (id: string) => void }) {
  const selectedVol = releasedVolumes.find(v => v.id === volumeId)
  return (
    <FormField
      label="Volume"
      htmlFor="notebook-volume"
      helperText={selectedVol ? selectedVol.work_dir : 'Attach to a released volume to recover existing data, or leave blank to provision a new one.'}
    >
      <Select
        items={[
          { value: '', label: 'new volume' },
          ...releasedVolumes.map(v => ({
            value: v.id,
            label: <>{v.label}&nbsp;·&nbsp;<span className="font-mono text-xs">{v.id.slice(0, 8)}</span>{v.work_dir ? `  ${v.work_dir}` : ''}</>,
          })),
        ]}
        value={volumeId}
        onValueChange={v => onChange(v ?? '')}
      >
        <SelectTrigger id="notebook-volume" size="sm" className="h-8 text-sm"><SelectValue placeholder="— new volume —" /></SelectTrigger>
        <SelectContent>
          <SelectItem value="">new volume</SelectItem>
          {releasedVolumes.map(v => (
            <SelectItem key={v.id} value={v.id}>
              {v.label}&nbsp;·&nbsp;<span className="font-mono text-xs">{v.id.slice(0, 8)}</span>
              {v.work_dir ? `  ${v.work_dir}` : ''}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </FormField>
  )
}
