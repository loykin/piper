import { useEffect, useRef, useState } from 'react'
import { ArrowDown, ArrowUp, FolderOpen, Plus, Trash2, X } from 'lucide-react'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { ShellMirror } from '@/components/ui/shell-mirror'
import { EnvVarEditor } from '@/features/credentials/components/EnvVarEditor'
import type { PipelineStepDraft, PipelineTaskType } from '../../editor'
import { RUNTIME_ITEMS, SOURCE_LABELS } from '../../editorModel'
import type { TaskDraft } from '../../useTaskDraft'
import { ArtifactSection } from './ArtifactSection'
import { DepBrowseDropdown, FileBrowseDropdown } from './BrowseDropdowns'
import { PairSection } from './PairSection'

/** The editor's right pane: every field of the step being edited. */
export function TaskEditorPane({
  task, index, taskCount, draft, canBrowse, volumeFiles, onClose,
}: {
  task: PipelineStepDraft
  index: number
  taskCount: number
  draft: TaskDraft
  canBrowse: boolean
  volumeFiles: string[]
  onClose: () => void
}) {
  const {
    updateTask, updateTaskDriver, updateDep, addDep, removeDep, removeTask, moveTask,
    updateArtifactField, updateParamField, updateEnvField,
    addArtifactRow, removeArtifactRow, addParamRow, addEnvRow, removeParamRow, removeEnvRow,
  } = draft
  const [fileBrowserOpen, setFileBrowserOpen] = useState(false)
  const fileBrowserRef = useRef<HTMLDivElement>(null)
  const [artifactBrowseKey, setArtifactBrowseKey] = useState<string | null>(null)
  const artifactBrowseRef = useRef<HTMLDivElement>(null)
  const [browseQuery, setBrowseQuery] = useState('')

  // Close whichever file browser is open on a click outside it.
  useEffect(() => {
    if (!fileBrowserOpen && !artifactBrowseKey) return
    const handler = (e: MouseEvent) => {
      if (fileBrowserRef.current && !fileBrowserRef.current.contains(e.target as Node)) setFileBrowserOpen(false)
      if (artifactBrowseRef.current && !artifactBrowseRef.current.contains(e.target as Node)) setArtifactBrowseKey(null)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [fileBrowserOpen, artifactBrowseKey])

  return (
      <div className="flex h-full flex-col">
        <div className="flex shrink-0 items-center justify-between border-b border-border px-4 py-3">
          <div>
            <h2 className="text-sm font-semibold">Task Editor</h2>
            <p className="truncate text-xs text-muted-foreground">{task.name}</p>
          </div>
          <div className="flex items-center gap-1">
            <IconButton icon={<ArrowUp />} label="Move Up" onClick={() => moveTask(index, -1)} disabled={index === 0} />
            <IconButton icon={<ArrowDown />} label="Move Down" onClick={() => moveTask(index, 1)} disabled={index === taskCount - 1} />
            <IconButton icon={<Trash2 />} label="Delete Task" onClick={() => removeTask(index)} className="text-destructive hover:bg-destructive/10" />
            <IconButton icon={<X />} label="Close" onClick={onClose} />
          </div>
        </div>

        <div className="flex-1 space-y-4 overflow-y-auto p-4">
          <div className="grid gap-3 sm:grid-cols-2">
            <div>
              <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Task Name</label>
              <Input value={task.name} onChange={e => updateTask(index, { name: e.target.value })} />
            </div>
            <div>
              <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Task Type</label>
              <Select
                items={[
                  { value: 'command', label: 'Command' },
                  { value: 'python', label: 'Python' },
                  { value: 'notebook', label: 'Notebook' },
                ]}
                value={task.type}
                onValueChange={value => updateTask(index, { type: value as PipelineTaskType })}
              >
                <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="command">Command</SelectItem>
                  <SelectItem value="python">Python</SelectItem>
                  <SelectItem value="notebook">Notebook</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>

          {(task.type === 'notebook' || task.type === 'python' || task.type === 'command') && (
            <div>
              <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">
                {SOURCE_LABELS[task.type]}
              </label>
              {task.type === 'command' && (
                <p className="mb-1 text-xs text-muted-foreground">
                  Optional. Path within the repo the command should treat as $PIPER_SCRIPT_PATH — a directory is fine. Leave empty to run from the repo root.
                </p>
              )}
              <div ref={fileBrowserRef} className="relative">
                <div className="flex gap-1.5">
                  <Input
                    value={task.sourcePath}
                    onChange={e => updateTask(index, { sourcePath: e.target.value })}
                    placeholder={task.type === 'notebook' ? 'workbook.ipynb' : task.type === 'python' ? 'scripts/train.py' : 'scripts'}
                    aria-invalid={(task.type === 'notebook' || task.type === 'python') && !task.sourcePath.trim()}
                  />
                  {canBrowse && (
                    <IconButton
                      icon={<FolderOpen />}
                      label="Browse Files in Volume"
                      onClick={() => { setBrowseQuery(''); setFileBrowserOpen(o => !o) }}
                    />
                  )}
                </div>
                {fileBrowserOpen && canBrowse && (
                  <FileBrowseDropdown
                    ext={task.type === 'notebook' ? '.ipynb' : '.py'}
                    files={volumeFiles.filter(f => f.endsWith(task.type === 'notebook' ? '.ipynb' : '.py'))}
                    query={browseQuery}
                    onQueryChange={setBrowseQuery}
                    onSelect={f => { updateTask(index, { sourcePath: f }); setFileBrowserOpen(false) }}
                  />
                )}
              </div>
              {(task.type === 'notebook' || task.type === 'python') && !task.sourcePath.trim() && (
                <p className="mt-1 text-xs text-destructive">{SOURCE_LABELS[task.type]} is required.</p>
              )}
            </div>
          )}

          {task.type !== 'notebook' && (
            <div>
              <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Command</label>
              <ShellMirror
                value={task.command.join('\n')}
                onChange={e => updateTask(index, { command: e.target.value.split('\n') })}
                minHeight="6rem"
                placeholder={task.type === 'python' ? 'sh\n-c\npython3 "$PIPER_SCRIPT_PATH"' : 'echo\nhello'}
              />
            </div>
          )}

          {(task.type === 'notebook' || task.type === 'python') && (
            <div>
              <div className="mb-2 flex items-center justify-between">
                <label className="block text-[11px] uppercase tracking-wider text-muted-foreground">Source Dependencies</label>
                <Button variant="outline" size="sm" onClick={() => addDep(index)}><Plus size={14} className="mr-1.5" /> Add</Button>
              </div>
              {task.deps.length === 0 ? (
                <p className="text-xs text-muted-foreground">No extra files or directories. Entry point only.</p>
              ) : (
                <div className="space-y-2">
                  {task.deps.map((dep, di) => {
                    const browseKey = `dep-${di}`
                    const isBrowseOpen = artifactBrowseKey === browseKey
                    return (
                      <div key={di} ref={isBrowseOpen ? artifactBrowseRef : null} className="relative">
                        <div className="flex gap-2">
                          <Input
                            value={dep}
                            placeholder="models/  or  utils/helper.py"
                            onChange={e => updateDep(index, di, e.target.value)}
                          />
                          {canBrowse && (
                            <IconButton
                              icon={<FolderOpen />}
                              label="Browse Volume Files"
                              onClick={() => { setBrowseQuery(''); setArtifactBrowseKey(k => k === browseKey ? null : browseKey) }}
                            />
                          )}
                          <IconButton icon={<Trash2 />} label="Remove" onClick={() => removeDep(index, di)} className="text-destructive hover:bg-destructive/10" />
                        </div>
                        {isBrowseOpen && (
                          <DepBrowseDropdown
                            files={volumeFiles}
                            query={browseQuery}
                            onQueryChange={setBrowseQuery}
                            onSelect={f => { updateDep(index, di, f); setArtifactBrowseKey(null) }}
                          />
                        )}
                      </div>
                    )
                  })}
                </div>
              )}
              <p className="mt-1 text-[11px] text-muted-foreground">Append <code>/</code> for directories (e.g. <code>models/</code>). All contents are included in the snapshot.</p>
            </div>
          )}

          <PairSection
            label="Parameters"
            emptyText="No parameters."
            keyPlaceholder="name"
            items={task.params}
            onAdd={() => addParamRow(index)}
            onRemove={i => removeParamRow(index, i)}
            onUpdate={(i, patch) => updateParamField(index, i, patch)}
          />

          <EnvVarEditor
            label="Environment"
            emptyText="No env overrides."
            items={task.env}
            onAdd={() => addEnvRow(index)}
            onRemove={i => removeEnvRow(index, i)}
            onUpdate={(i, patch) => updateEnvField(index, i, patch)}
          />

          <ArtifactSection
            label="Inputs"
            kind="inputs"
            items={task.inputs}
            canBrowse={canBrowse}
            volumeFiles={volumeFiles}
            activeBrowseKey={artifactBrowseKey}
            browseQuery={browseQuery}
            browseRef={artifactBrowseRef}
            onAdd={() => addArtifactRow(index, 'inputs')}
            onRemove={i => removeArtifactRow(index, 'inputs', i)}
            onUpdate={(i, patch) => updateArtifactField(index, 'inputs', i, patch)}
            onBrowseToggle={key => { setBrowseQuery(''); setArtifactBrowseKey(k => k === key ? null : key) }}
            onBrowseQueryChange={setBrowseQuery}
            onBrowseSelect={(i, f) => { updateArtifactField(index, 'inputs', i, { path: f }); setArtifactBrowseKey(null) }}
          />

          <ArtifactSection
            label="Outputs"
            kind="outputs"
            items={task.outputs}
            canBrowse={canBrowse}
            volumeFiles={volumeFiles}
            activeBrowseKey={artifactBrowseKey}
            browseQuery={browseQuery}
            browseRef={artifactBrowseRef}
            onAdd={() => addArtifactRow(index, 'outputs')}
            onRemove={i => removeArtifactRow(index, 'outputs', i)}
            onUpdate={(i, patch) => updateArtifactField(index, 'outputs', i, patch)}
            onBrowseToggle={key => { setBrowseQuery(''); setArtifactBrowseKey(k => k === key ? null : key) }}
            onBrowseQueryChange={setBrowseQuery}
            onBrowseSelect={(i, f) => { updateArtifactField(index, 'outputs', i, { path: f }); setArtifactBrowseKey(null) }}
          />

          <div>
           <h2 className="mb-3 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Runtime Override</h2>
           <div className="space-y-3">
            <div>
              <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Runtime</label>
              <Select
                items={[{ value: '__inherit__', label: 'Inherit' }, ...RUNTIME_ITEMS]}
                value={task.driver.placementRuntime || '__inherit__'}
                onValueChange={value => updateTaskDriver(index, {
                  placementRuntime: value === '__inherit__' ? '' : (value ?? ''),
                })}
              >
                <SelectTrigger size="sm" className="w-full"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="__inherit__">Inherit</SelectItem>
                  <SelectItem value="baremetal">Bare metal</SelectItem>
                  <SelectItem value="docker">Docker</SelectItem>
                  <SelectItem value="k8s">Kubernetes</SelectItem>
                </SelectContent>
              </Select>
              <p className="mt-1 text-xs text-muted-foreground">Leave as Inherit to use pipeline defaults.</p>
            </div>
            {task.driver.placementRuntime === 'k8s' && (
              <div className="grid gap-3 sm:grid-cols-2">
                <div>
                  <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Container Image</label>
                  <Input
                    value={task.driver.k8sImage}
                    onChange={e => updateTaskDriver(index, { k8sImage: e.target.value })}
                  />
                </div>
                <div>
                  <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Namespace</label>
                  <Input
                    value={task.driver.k8sNamespace}
                    onChange={e => updateTaskDriver(index, { k8sNamespace: e.target.value })}
                  />
                </div>
              </div>
            )}
            {task.driver.placementRuntime === 'docker' && (
              <div>
                <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Container Image</label>
                <Input
                  value={task.driver.dockerImage}
                  onChange={e => updateTaskDriver(index, { dockerImage: e.target.value })}
                />
              </div>
            )}
           </div>
          </div>

          <div className="grid grid-cols-3 gap-3">
            <div>
              <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">CPU</label>
              <Input value={task.cpu} onChange={e => updateTask(index, { cpu: e.target.value })} placeholder="500m" />
            </div>
            <div>
              <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">Memory</label>
              <Input value={task.memory} onChange={e => updateTask(index, { memory: e.target.value })} placeholder="1Gi" />
            </div>
            <div>
              <label className="mb-1 block text-[11px] uppercase tracking-wider text-muted-foreground">GPU</label>
              <Input value={task.gpu} onChange={e => updateTask(index, { gpu: e.target.value })} placeholder="1" />
            </div>
          </div>
        </div>
      </div>
  )
}
