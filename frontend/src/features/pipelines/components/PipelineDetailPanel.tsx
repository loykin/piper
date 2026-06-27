import { useState } from 'react'
import { CalendarClock, CopyPlus, Play, Trash2, X } from 'lucide-react'
import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { YamlMirror } from '@/components/ui/yaml-mirror'
import { useUpdateTemplateMeta } from '../hooks'
import type { PipelineTemplate } from '../types'

interface Props {
  template: PipelineTemplate
  onRun: (t: PipelineTemplate) => void
  onDeploy: (t: PipelineTemplate) => void
  onNewVersion: (t: PipelineTemplate) => void
  onDelete: (t: PipelineTemplate) => void
}

export function PipelineDetailPanel({ template: t, onRun, onDeploy, onNewVersion, onDelete }: Props) {
  const { close } = useSidePanel()
  const { mutate: updateMeta, isPending: saving } = useUpdateTemplateMeta()

  const [description, setDescription] = useState(t.description ?? '')
  const [tags, setTags] = useState<string[]>(t.tags ?? [])
  const [tagInput, setTagInput] = useState('')
  const [dirty, setDirty] = useState(false)

  function handleDescriptionChange(v: string) {
    setDescription(v)
    setDirty(true)
  }

  function addTag(raw: string) {
    const tag = raw.trim()
    if (!tag || tags.includes(tag)) return
    const next = [...tags, tag]
    setTags(next)
    setTagInput('')
    setDirty(true)
  }

  function removeTag(tag: string) {
    setTags(tags.filter(t => t !== tag))
    setDirty(true)
  }

  function save() {
    updateMeta({ id: t.id, req: { description, tags } }, { onSuccess: () => setDirty(false) })
  }

  const closeBtn = (
    <Button variant="ghost" size="icon-sm" onClick={() => void close()}>
      <X className="h-3.5 w-3.5" />
    </Button>
  )

  return (
    <PanelTemplate
      eyebrow={`v${t.version}`}
      title={t.name}
      actions={
        <div className="flex items-center gap-1">
          <IconButton icon={<Play />} label="Run" onClick={() => { onRun(t); void close() }} />
          <IconButton icon={<CalendarClock />} label="Deploy to schedule" onClick={() => { onDeploy(t); void close() }} />
          <IconButton icon={<CopyPlus />} label={`New version from v${t.version}`} onClick={() => { onNewVersion(t); void close() }} />
          <IconButton
            icon={<Trash2 />} label="Delete"
            onClick={() => { onDelete(t); void close() }}
            className="text-destructive hover:bg-destructive/10"
          />
          {closeBtn}
        </div>
      }
    >
      <PanelTemplate.Section title="Details">
        <dl className="grid grid-cols-2 gap-3">
          <div>
            <dt className="text-xs text-muted-foreground">Version</dt>
            <dd className="mt-0.5 text-sm font-medium">v{t.version}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Submitted</dt>
            <dd className="mt-0.5 text-xs">{new Date(t.created_at).toLocaleString()}</dd>
          </div>
          {t.volume_id && (
            <div className="col-span-2">
              <dt className="text-xs text-muted-foreground">Volume</dt>
              <dd className="mt-0.5 font-mono text-xs">{t.volume_id}</dd>
            </div>
          )}
          <div className="col-span-2">
            <dt className="text-xs text-muted-foreground">Version ID</dt>
            <dd className="mt-0.5 font-mono text-xs break-all">{t.id}</dd>
          </div>
          <div className="col-span-2">
            <dt className="text-xs text-muted-foreground">Snapshot ID</dt>
            <dd className="mt-0.5 font-mono text-xs break-all">{t.snapshot_id}</dd>
          </div>
        </dl>
      </PanelTemplate.Section>

      <PanelTemplate.Section title="Description">
        <Input
          value={description}
          onChange={e => handleDescriptionChange(e.target.value)}
          placeholder="Describe this pipeline…"
          className="text-sm"
        />
      </PanelTemplate.Section>

      <PanelTemplate.Section title="Tags">
        <div className="flex flex-wrap gap-1.5 mb-2">
          {tags.map(tag => (
            <Badge key={tag} variant="secondary" className="gap-1 pr-1">
              {tag}
              <button type="button" onClick={() => removeTag(tag)} className="ml-0.5 text-muted-foreground hover:text-foreground">
                <X className="h-3 w-3" />
              </button>
            </Badge>
          ))}
        </div>
        <Input
          value={tagInput}
          onChange={e => setTagInput(e.target.value)}
          onKeyDown={e => {
            if (e.key === 'Enter' || e.key === ',') {
              e.preventDefault()
              addTag(tagInput)
            }
          }}
          onBlur={() => { if (tagInput.trim()) addTag(tagInput) }}
          placeholder="Add tag, press Enter…"
          className="text-sm"
        />
      </PanelTemplate.Section>

      {dirty && (
        <div className="px-4 pb-2">
          <Button size="sm" onClick={save} disabled={saving} className="w-full">
            {saving ? 'Saving…' : 'Save changes'}
          </Button>
        </div>
      )}

      <PanelTemplate.Section title="Pipeline YAML">
        <YamlMirror value={t.yaml} readOnly />
      </PanelTemplate.Section>
    </PanelTemplate>
  )
}
