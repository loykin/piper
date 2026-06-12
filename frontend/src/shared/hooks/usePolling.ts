// Lightweight polling hook for cases where React Query isn't suitable
import { useEffect, useRef } from 'react'

export function usePolling(fn: () => void, intervalMs: number, enabled = true) {
  const fnRef = useRef(fn)
  // Updating ref outside render is safe; lint rule is overly strict here.
  // eslint-disable-next-line react-hooks/refs
  fnRef.current = fn

  useEffect(() => {
    if (!enabled) return
    const iv = setInterval(() => fnRef.current(), intervalMs)
    return () => clearInterval(iv)
  }, [intervalMs, enabled])
}
