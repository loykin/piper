import { useState } from 'react'
import { CheckCircle, FlaskConical, XCircle } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import type { Credential, TestCredentialResult } from '@/features/credentials/types'
import type { UseMutationResult } from '@tanstack/react-query'

export default function TestCredentialDialog({
  target,
  testCredential,
  onClose,
}: {
  target: Credential | null
  testCredential: UseMutationResult<TestCredentialResult, Error, { name: string; req: { repo?: string } }>
  onClose: () => void
}) {
  const [repo, setRepo] = useState('')
  const [result, setResult] = useState<TestCredentialResult | null>(null)

  function handleOpenChange(open: boolean) {
    if (!open) { setRepo(''); setResult(null); testCredential.reset(); onClose() }
  }

  async function runTest() {
    if (!target) return
    setResult(null)
    try {
      const res = await testCredential.mutateAsync({ name: target.name, req: { repo: repo.trim() || undefined } })
      setResult(res)
    } catch (err) {
      setResult({ ok: false, message: err instanceof Error ? err.message : String(err) })
    }
  }

  return (
    <Dialog open={!!target} onOpenChange={handleOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <FlaskConical className="size-4" />
            Test {target?.name}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="test-repo">Repository URL</Label>
            <Input
              id="test-repo"
              value={repo}
              onChange={e => setRepo(e.target.value)}
              placeholder="https://github.com/myorg/myrepo"
              className="font-mono text-sm"
            />
          </div>
          {result && (
            <div className="flex items-start gap-2 rounded-md border p-3 text-sm">
              {result.ok
                ? <CheckCircle className="mt-0.5 size-4 shrink-0 text-primary" />
                : <XCircle className="mt-0.5 size-4 shrink-0" />}
              <div className="min-w-0 space-y-1">
                <Badge variant={result.ok ? 'default' : 'destructive'}>{result.ok ? 'Passed' : 'Failed'}</Badge>
                <p className="break-words text-muted-foreground">{result.message}</p>
              </div>
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>Close</Button>
          <Button
            onClick={() => void runTest()}
            disabled={testCredential.isPending || !repo.trim()}
          >
            {testCredential.isPending ? 'Testing...' : 'Run Test'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
