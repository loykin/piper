import { useState } from 'react'
import { DataBodyTemplate } from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { YamlMirror } from '@/components/ui/yaml-mirror'
import { useSetWorkerPodPolicy } from '@/features/workers/hooks'

// ── Example templates ────────────────────────────────────────────────────────

const EXAMPLES: { label: string; description: string; yaml: string }[] = [
  {
    label: 'GPU (NVIDIA)',
    description: 'Schedule notebooks on NVIDIA GPU nodes with the nvidia runtime.',
    yaml: `spec:
  nodeSelector:
    nvidia.com/gpu: "true"
  tolerations:
    - key: dedicated
      operator: Equal
      value: gpu
      effect: NoSchedule
  runtimeClassName: nvidia
`,
  },
  {
    label: 'Spot instances',
    description: 'Allow notebooks to land on spot/preemptible nodes.',
    yaml: `spec:
  tolerations:
    - key: cloud.google.com/gke-spot
      operator: Exists
      effect: NoSchedule
`,
  },
  {
    label: 'FUSE device',
    description: 'Mount the host /dev/fuse device for FUSE-based filesystems.',
    yaml: `spec:
  volumes:
    - name: fuse
      hostPath:
        path: /dev/fuse
  containers:
    - name: notebook
      securityContext:
        privileged: false
        capabilities:
          add: [SYS_ADMIN]
      volumeMounts:
        - name: fuse
          mountPath: /dev/fuse
`,
  },
  {
    label: 'Service account',
    description: 'Attach a Kubernetes service account to all notebooks on this worker.',
    yaml: `spec:
  serviceAccountName: notebook-runner
`,
  },
]

// ── Props ────────────────────────────────────────────────────────────────────

interface PodPolicyFormProps {
  /** If provided, form is in edit mode for this worker. Worker ID field is hidden. */
  workerId?: string
  /** Pre-fill the YAML editor (edit mode). */
  initialYaml?: string
  onSaved: (workerId: string) => void
  onCancel: () => void
}

// ── Form ─────────────────────────────────────────────────────────────────────

export function PodPolicyForm({ workerId: initialWorkerId, initialYaml, onSaved, onCancel }: PodPolicyFormProps) {
  const isEdit = !!initialWorkerId
  const [workerId, setWorkerId] = useState(initialWorkerId ?? '')
  const [yaml, setYaml] = useState(initialYaml ?? '')
  const [error, setError] = useState('')
  const { mutateAsync: save, isPending: saving } = useSetWorkerPodPolicy()

  async function handleSave() {
    setError('')
    const id = workerId.trim()
    if (!id) { setError('Worker ID is required.'); return }
    if (!yaml.trim()) { setError('Pod template YAML is required.'); return }
    try {
      await save({ workerId: id, yaml })
      onSaved(id)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Save failed.')
    }
  }

  return (
    <div className="max-w-2xl space-y-6">

      {/* Worker ID */}
      {!isEdit && (
        <DataBodyTemplate.Field
          label="Worker ID"
          description="The stable UUID assigned to the worker on first connection. Find it on the Workers page when the worker is online, or in the worker's state-dir file."
        >
          <Input
            value={workerId}
            onChange={e => setWorkerId(e.target.value)}
            placeholder="e.g. 7f3a1b2c-4d5e-6f7a-8b9c-0d1e2f3a4b5c"
            className="font-mono"
          />
        </DataBodyTemplate.Field>
      )}

      {/* Examples */}
      <DataBodyTemplate.Group layout="stacked" variant="bordered" title="Templates">
        <p className="mb-2 text-xs text-muted-foreground">
          Start from a common pattern. Clicking a template overwrites the editor below.
        </p>
        <div className="flex flex-wrap gap-2">
          {EXAMPLES.map(ex => (
            <Button
              key={ex.label}
              type="button"
              variant="outline"
              size="sm"
              title={ex.description}
              onClick={() => setYaml(ex.yaml)}
            >
              {ex.label}
            </Button>
          ))}
        </div>
      </DataBodyTemplate.Group>

      {/* Pod template YAML */}
      <DataBodyTemplate.Field
        label="Pod Template"
        description="A partial PodTemplateSpec applied to every notebook dispatched to this worker. Manifest pod_template fields override conflicts; Piper controls container name, image, ports, and PVC mounts."
      >
        <YamlMirror
          value={yaml}
          onChange={e => setYaml(e.target.value)}
          className="min-h-[22rem]"
        />
      </DataBodyTemplate.Field>

      {error && (
        <p className="text-sm text-destructive">{error}</p>
      )}

      <div className="flex gap-3">
        <Button onClick={handleSave} disabled={saving}>
          {saving ? 'Saving…' : 'Save policy'}
        </Button>
        <Button variant="ghost" onClick={onCancel} disabled={saving}>
          Cancel
        </Button>
      </div>
    </div>
  )
}
