import { useCallback, useState } from 'react'

// Shared "which verb is pending confirmation" state for a detail view's
// confirm AlertDialog (e.g. cancel/delete on a run, stop/delete on a
// service). See docs/frontend/develop.md's Resource List Interaction Pattern
// (rule 8) for the AlertDialog contract this backs.
export function useConfirmAction<TVerb extends string>() {
  const [action, setAction] = useState<TVerb | null>(null)
  const [error, setError] = useState('')

  const requestAction = useCallback((v: TVerb) => {
    setError('')
    setAction(v)
  }, [])

  const cancel = useCallback(() => {
    setAction(null)
    setError('')
  }, [])

  const confirm = useCallback(async (fn: (v: TVerb) => Promise<void>) => {
    if (!action) return
    setError('')
    try {
      await fn(action)
      setAction(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [action])

  return { action, open: action !== null, error, requestAction, cancel, confirm }
}
