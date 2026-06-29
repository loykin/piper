import { useState } from 'react'
import { CheckCircle, FlaskConical, XCircle } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import type { Connection, TestConnectionResult } from '@/features/connections/types'
import type { UseMutationResult } from '@tanstack/react-query'

export default function TestConnectionDialog({
  target,
  testConnection,
  onClose,
}: {
  target: Connection | null
  testConnection: UseMutationResult<TestConnectionResult, Error, { name: string; req: { repo?: string } }>
  onClose: () => void
}) {
  const [repo, setRepo] = useState('')
  const [result, setResult] = useState<TestConnectionResult | null>(null)

  function handleOpenChange(open: boolean) {
    if (!open) { setRepo(''); setResult(null); testConnection.reset(); onClose() }
  }

  async function runTest() {
    if (!target) return
    setResult(null)
    try {
      const res = await testConnection.mutateAsync({ name: target.name, req: { repo: repo.trim() || undefined } })
      setResult(res)
    } catch (err) {
      setResult({ ok: false, message: err instanceof Error ? err.message : String(err) })
    }
  }

  const needsRepo = target?.type === 'git'

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
          {needsRepo && (
            <div className="space-y-1.5">
              <Label htmlFor="test-repo">Repository URL</Label>
              <Input
                id="test-repo"
                value={repo}
                onChange={e => setRepo(e.target.value)}
                placeholder="https://github.com/myorg/myrepo"
                className="font-mono text-sm"
              />
              <p className="text-xs text-muted-foreground">
                Required for git connections. The test runs <code>git ls-remote</code> against this URL.
              </p>
            </div>
          )}
          {result && (
            <div className={`flex items-start gap-2 rounded-md border p-3 text-sm ${result.ok ? 'border-green-500/30 bg-green-500/10 text-green-700 dark:text-green-400' : 'border-destructive/30 bg-destructive/10 text-destructive'}`}>
              {result.ok
                ? <CheckCircle className="mt-0.5 size-4 shrink-0" />
                : <XCircle className="mt-0.5 size-4 shrink-0" />}
              <span>{result.message}</span>
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>Close</Button>
          <Button
            onClick={() => void runTest()}
            disabled={testConnection.isPending || (needsRepo && !repo.trim())}
          >
            {testConnection.isPending ? 'Testing...' : 'Run Test'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
