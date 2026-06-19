// runs feature — Artifact panel component
import { useState } from 'react'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { DataPage } from '@loykin/designkit'
import { artifactDownloadURL } from '../api'
import { useOpenViewer } from '@/features/viewers/hooks'
import type { ArtifactFile, ArtifactEntry, StepArtifacts } from '../types'

function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`
  return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`
}

const viewerLabel: Record<string, string> = {
  tensorboard: 'Open TensorBoard',
  html:        'Open Report',
  table:       'Preview Table',
  image:       'View Image',
}

interface OpenButtonProps {
  runId: string
  step: string
  art: ArtifactEntry
}

function OpenButton({ runId, step, art }: OpenButtonProps) {
  const label = viewerLabel[art.type ?? ''] ?? `Open ${art.type}`
  const { mutate, isPending } = useOpenViewer(runId, step, art.name)
  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      disabled={isPending}
      onClick={() => mutate({ type: art.type! })}
      className="h-6 px-2 text-xs"
    >
      {isPending ? 'Opening…' : label}
    </Button>
  )
}

interface ArtifactPanelProps {
  runId: string
  artifacts: StepArtifacts[]
}

export function ArtifactPanel({ runId, artifacts }: ArtifactPanelProps) {
  const [preview, setPreview] = useState<{ title: string; text: string } | null>(null)

  if (artifacts.length === 0) return null

  const previewArtifact = async (step: string, art: string, file: ArtifactFile) => {
    if (file.size > 128 * 1024) {
      alert('Preview is limited to files up to 128 KB.')
      return
    }
    const res = await fetch(artifactDownloadURL(runId, step, art, file.path))
    if (!res.ok) {
      alert(`Preview failed: ${res.status}`)
      return
    }
    setPreview({ title: `${step}/${art}/${file.path}`, text: await res.text() })
  }

  return (
    <>
      <DataPage.Group surface="bordered" className="mb-4">
        <DataPage.GroupHeader title="Artifacts" className="px-4 pt-3" />
        <div className="divide-y divide-border">
          {artifacts.map((sa) =>
            sa.artifacts.map((art) => (
              <div key={`${sa.step}/${art.name}`} className="px-4 py-3">
                <div className="mb-2 flex items-center gap-2">
                  <span className="rounded bg-muted px-2 py-0.5 font-mono text-xs text-muted-foreground">{sa.step}</span>
                  <span className="text-sm font-medium text-foreground">{art.name}</span>
                  <span className="text-xs text-muted-foreground">({art.files.length} file{art.files.length !== 1 ? 's' : ''})</span>
                  {art.type ? <OpenButton runId={runId} step={sa.step} art={art} /> : null}
                </div>
                <div className="space-y-1">
                  {art.files.map((f) => (
                    <div key={f.path} className="flex items-center justify-between rounded px-2 py-1 hover:bg-accent">
                      <span className="font-mono text-xs text-muted-foreground">{f.path}</span>
                      <div className="flex items-center gap-4">
                        <span className="text-xs text-muted-foreground">{formatFileSize(f.size)}</span>
                        <span className="text-xs text-muted-foreground">{new Date(f.modified_at).toLocaleString()}</span>
                        <a
                          href={artifactDownloadURL(runId, sa.step, art.name, f.path)}
                          download
                          className="text-xs text-primary hover:text-primary/80"
                        >
                          Download
                        </a>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          onClick={() => void previewArtifact(sa.step, art.name, f)}
                          className="h-auto px-1 py-0 text-xs"
                        >
                          Preview
                        </Button>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            ))
          )}
        </div>
      </DataPage.Group>

      <Dialog open={!!preview} onOpenChange={(open) => { if (!open) setPreview(null) }}>
        <DialogContent className="max-w-4xl">
          <DialogHeader>
            <DialogTitle className="font-mono text-xs text-muted-foreground">{preview?.title}</DialogTitle>
          </DialogHeader>
          <pre className="max-h-[68vh] overflow-auto text-xs leading-5 text-foreground">{preview?.text}</pre>
        </DialogContent>
      </Dialog>
    </>
  )
}
