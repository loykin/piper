// Lightweight polling hook for cases where React Query isn't suitable
import { useEffect, useRef } from 'react'

export function usePolling(fn: () => void, intervalMs: number, enabled = true) {
  const fnRef = useRef(fn)
  fnRef.current = fn

  useEffect(() => {
    if (!enabled) return
    const iv = setInterval(() => fnRef.current(), intervalMs)
    return () => clearInterval(iv)
  }, [intervalMs, enabled])
}
