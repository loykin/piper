import { useRef, useState } from 'react'
import { FolderOpen, Save } from 'lucide-react'
import { FormField } from '@loykin/designkit'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useUploadObject } from '@/features/storage/hooks'
import { errorMessage, fmtBytes } from '@/lib/format'

// ── Upload Object ───────────────────────────────────────────────────────────
// A narrowly-scoped value-collection action (file + optional key), not an
// entity worth its own page — matches the Modal destination in the
// form-workflow contract, and mirrors credentials' TestCredentialDialog/
// RotateCredentialDialog. Triggered from Uploaded Objects' own toolbar since
// it's that list's create action, not a permanent fixture above the list.

// Mirrors serve.go's maxBlobRequestBodyBytes — the built-in store's blob
// upload cap. Kept as a client-side estimate only; the server response is
// still the source of truth if this ever drifts.
const MAX_UPLOAD_BYTES = 4 * 1024 * 1024 * 1024


export function UploadObjectDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const uploadObject = useUploadObject()
  const [uploadKey, setUploadKey] = useState('')
  const [uploadFile, setUploadFile] = useState<File | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const fileTooLarge = uploadFile !== null && uploadFile.size > MAX_UPLOAD_BYTES

  function handleOpenChange(next: boolean) {
    if (!next) {
      setUploadKey('')
      setUploadFile(null)
      uploadObject.reset()
      if (fileInputRef.current) fileInputRef.current.value = ''
    }
    onOpenChange(next)
  }

  async function handleUpload() {
    if (!uploadFile) return
    try {
      await uploadObject.mutateAsync({ file: uploadFile, key: uploadKey.trim() || uploadFile.name })
      handleOpenChange(false)
    } catch {
      // surfaced below via uploadObject.error
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Upload Object</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <FormField
            label="Object key"
            htmlFor="upload-object-key"
            helperText="Leave empty to use the selected file name."
          >
            <Input
              id="upload-object-key"
              value={uploadKey}
              onChange={e => setUploadKey(e.target.value)}
              placeholder="runs/run-123/model/model.bin"
            />
          </FormField>

          <FormField label="File" htmlFor="upload-object-file">
            <Input
              ref={fileInputRef}
              id="upload-object-file"
              type="file"
              className="hidden"
              onChange={e => setUploadFile(e.target.files?.[0] ?? null)}
            />
            <div className="flex items-center gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => fileInputRef.current?.click()}
              >
                <FolderOpen className="mr-2 size-4" />
                Choose file
              </Button>
              <span className="text-sm text-muted-foreground">
                {uploadFile ? uploadFile.name : 'No file chosen'}
              </span>
            </div>
          </FormField>

          {fileTooLarge && uploadFile && (
            <p className="text-sm text-destructive">
              {fmtBytes(uploadFile.size)} exceeds the {fmtBytes(MAX_UPLOAD_BYTES)} upload limit.
            </p>
          )}
          {uploadObject.isError && (
            <p className="text-sm text-destructive">
              {errorMessage(uploadObject.error, 'Upload failed.')}
            </p>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => handleOpenChange(false)}>Cancel</Button>
          <Button onClick={() => void handleUpload()} disabled={!uploadFile || fileTooLarge || uploadObject.isPending}>
            <Save className="mr-2 size-4" />
            {uploadObject.isPending ? 'Uploading…' : 'Upload'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
