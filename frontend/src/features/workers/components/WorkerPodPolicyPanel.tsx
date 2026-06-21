import { useEffect, useState } from 'react'
import { stringify as yamlStringify, parse as yamlParse } from 'yaml'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { YamlMirror } from '@/components/ui/yaml-mirror'
import type { Worker } from '@/features/workers/types'
import {
  useWorkerPodPolicy,
  useSetWorkerPodPolicy,
  useDeleteWorkerPodPolicy,
} from '@/features/workers/hooks'

function policyToYaml(podTemplate: Record<string, unknown>): string {
  try {
    return yamlStringify(podTemplate, { indent: 2 })
  } catch {
    return ''
  }
}

function isEmptyPodTemplate(yaml: string): boolean {
  if (!yaml.trim()) return true
  try {
    const parsed = yamlParse(yaml)
    return !parsed || Object.keys(parsed as object).length === 0
  } catch {
    return false
  }
}

export function WorkerPodPolicyPanel({ worker }: { worker: Worker }) {
  const { close } = useSidePanel()
  const { data: policy, isLoading } = useWorkerPodPolicy(worker.id)
  const { mutateAsync: save, isPending: saving } = useSetWorkerPodPolicy()
  const { mutateAsync: clear, isPending: clearing } = useDeleteWorkerPodPolicy()

  const [yaml, setYaml] = useState('')
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    if (!isLoading) {
      setYaml(policy?.pod_template ? policyToYaml(policy.pod_template) : '')
    }
  }, [policy, isLoading])

  const busy = saving || clearing

  const closeBtn = (
    <IconButton icon={<X />} label="Close" onClick={() => void close()} />
  )

	const isK8s = worker.infrastructure === 'k8s'

  const workerLabel = isK8s
    ? (worker.cluster_name || worker.hostname || worker.id)
    : (worker.hostname || worker.id)

  return (
    <PanelTemplate
      eyebrow={isK8s ? 'K8s Notebook Worker' : 'Notebook Worker'}
      title={workerLabel}
      actions={closeBtn}
    >
      <PanelTemplate.Section title="Info">
        <dl className="grid grid-cols-2 gap-3">
          <div>
            <dt className="text-xs text-muted-foreground">Hostname</dt>
            <dd className="mt-0.5 font-mono text-xs">{worker.hostname || '—'}</dd>
          </div>
          {worker.cluster_name && (
            <div>
              <dt className="text-xs text-muted-foreground">Cluster</dt>
              <dd className="mt-0.5 font-mono text-xs">{worker.cluster_name}</dd>
            </div>
          )}
          <div>
            <dt className="text-xs text-muted-foreground">Runtime</dt>
			<dd className="mt-0.5 text-xs">{worker.infrastructure === 'k8s' ? 'Kubernetes' : worker.infrastructure === 'docker' ? 'Docker' : 'Bare-metal'}</dd>
          </div>
        </dl>
      </PanelTemplate.Section>

      {isK8s && (
        <PanelTemplate.Section title="Pod Policy">
          <p className="mb-3 text-xs text-muted-foreground">
            Baseline <code>PodTemplateSpec</code> merged into every notebook dispatched to this worker.
            The notebook manifest's own <code>pod_template</code> overrides any conflicting fields.
          </p>
          <YamlMirror
            value={yaml}
            onChange={e => { setYaml(e.target.value); setSaved(false) }}
            className="min-h-[20rem]"
          />
          <div className="mt-3 flex items-center gap-2">
            <Button
              size="sm"
              disabled={busy}
              onClick={async () => {
                await save({ workerId: worker.id, yaml: yaml || '{}' })
                setSaved(true)
              }}
            >
              {saving ? 'Saving…' : 'Save'}
            </Button>
            {!isEmptyPodTemplate(yaml) && (
              <Button
                size="sm"
                variant="ghost"
                disabled={busy}
                onClick={async () => {
                  await clear(worker.id)
                  setYaml('')
                  setSaved(false)
                }}
              >
                {clearing ? 'Clearing…' : 'Clear'}
              </Button>
            )}
            {saved && (
              <span className="text-xs text-muted-foreground">Saved.</span>
            )}
          </div>
        </PanelTemplate.Section>
      )}
    </PanelTemplate>
  )
}
