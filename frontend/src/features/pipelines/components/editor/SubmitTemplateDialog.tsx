import { Upload } from 'lucide-react'
import { FormField, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@loykin/designkit'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import type { NotebookVolume } from '@/features/notebooks/types'

/**
 * Final confirmation before a pipeline template version is snapshotted.
 * The only input is which notebook volume (if any) supplies local sources.
 */
export function SubmitTemplateDialog({
  open, volumes, volumeId, onVolumeChange, error, submitting, onClose, onConfirm,
}: {
  open: boolean
  volumes: NotebookVolume[]
  volumeId: string
  onVolumeChange: (id: string) => void
  error?: string
  submitting: boolean
  onClose: () => void
  onConfirm: () => void
}) {
  return (
    <Dialog open={open} onOpenChange={next => { if (!next) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Submit Pipeline Template</DialogTitle>
          <DialogDescription>
            Submitting creates a new immutable snapshot of your source files in object storage.
            Each submission gets its own UUID — previous snapshots are untouched.
          </DialogDescription>
        </DialogHeader>
        <FormField
          label="Notebook Volume"
          htmlFor="submit-template-volume"
          helperText="Optional — required for local source steps."
          error={error}
        >
          <Select
            items={[{ value: '__none__', label: '— none —' }, ...volumes.map(v => ({ value: v.id, label: `${v.label} · ${v.work_dir}` }))]}
            value={volumeId || '__none__'}
            onValueChange={v => onVolumeChange(!v || v === '__none__' ? '' : v)}
          >
            <SelectTrigger id="submit-template-volume"><SelectValue placeholder="— none —" /></SelectTrigger>
            <SelectContent>
              <SelectItem value="__none__">— none —</SelectItem>
              {volumes.map(v => (
                <SelectItem key={v.id} value={v.id}>{v.label} · {v.work_dir}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </FormField>
        <DialogFooter>
          <Button variant="outline" size="sm" onClick={onClose} disabled={submitting}>Edit YAML</Button>
          <Button size="sm" onClick={onConfirm} disabled={submitting}>
            <Upload size={14} className="mr-1.5" />
            {submitting ? 'Submitting…' : 'Confirm Submit'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
