import { useCallback, useState } from 'react'
import { errorMessage } from '@/lib/format'

// Shared "which row is pending deletion" state for a resource list's
// destructive-confirm AlertDialog. See docs/frontend/develop.md's Resource
// List Interaction Pattern (rule 8) for the AlertDialog contract this backs.
export function useDeleteTarget<T>() {
  const [target, setTarget] = useState<T | null>(null)
  const [error, setError] = useState('')

  const requestDelete = useCallback((t: T) => {
    setError('')
    setTarget(t)
  }, [])

  const cancel = useCallback(() => {
    setTarget(null)
    setError('')
  }, [])

  const confirm = useCallback(async (fn: (t: T) => Promise<void>) => {
    if (!target) return
    setError('')
    try {
      await fn(target)
      setTarget(null)
    } catch (e) {
      setError(errorMessage(e))
    }
  }, [target])

  return { target, open: target !== null, error, requestDelete, cancel, confirm }
}
