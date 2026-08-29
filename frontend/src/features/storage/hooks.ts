import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import * as api from './api'
import { useProjectId } from '@/lib/projectContext'
import { ApiError } from '@/lib/api'

export const storageKeys = {
  settings: () => ['storage', 'settings'] as const,
  // Genuinely shorter than objects() below — used for broad invalidation so
  // it partial-matches every prefix variant, including the default ''.
  // objects(projectId) alone would end in `undefined`, which does NOT
  // partial-match a live query key ending in '' (React Query compares
  // element-by-element, and undefined !== '').
  objectsAll: (projectId: string) => ['storage', projectId, 'objects'] as const,
  objectsPaged: (projectId: string, limit: number, offset: number, prefix: string) =>
    ['storage', projectId, 'objects', 'paged', limit, offset, prefix] as const,
}

export function useStorageSettings() {
  return useQuery({
    queryKey: storageKeys.settings(),
    queryFn: api.getStorageSettings,
    // 403 here means "not a system admin on this instance" — retrying can
    // never fix that, and unlike a transient 5xx it shouldn't eat a retry
    // budget or leave the query stuck retrying.
    retry: (failureCount, error) => !(error instanceof ApiError && error.status >= 400 && error.status < 500) && failureCount < 1,
  })
}

export function useStorageObjectsPaged(limit: number, offset: number, prefix = '') {
  const projectId = useProjectId()
  return useQuery({
    queryKey: storageKeys.objectsPaged(projectId, limit, offset, prefix),
    queryFn: () => api.listStorageObjectsPaged(projectId, limit, offset, prefix),
    enabled: !!projectId,
    placeholderData: (prev) => prev,
    // retry: false is load-bearing, not just "don't bother retrying a 5xx".
    // TanStack Query's retryer only skips its focus check on a query's very
    // first attempt (canStart, network-only); every *retry* additionally
    // requires focusManager.isFocused() (canContinue) before it's allowed to
    // fire — see @tanstack/query-core's retryer.ts. If the backing storage
    // backend is down and the tab isn't focused/visible at the moment a
    // retry would run (backgrounded tab, alt-tabbed away, an embedded
    // preview pane, a screenshot/automation tool), the retryer parks itself
    // in fetchStatus 'paused' waiting for a focus/online event that may
    // never come — the query never reaches status 'error', isError stays
    // false forever, and this list's QueryErrorNotice (wired to isError)
    // never renders, reproducing exactly the "loading skeleton forever, no
    // error banner" bug this hook exists to fix. The default retry:1 (see
    // main.tsx) does not protect against this — it just delays the same
    // hang by one retry cycle. Disabling retries here means every fetch
    // (initial load or the notice's manual onRetry) goes through canStart
    // only, so a failure always reaches status 'error' immediately,
    // regardless of tab focus. QueryErrorNotice's Retry button remains the
    // way to try again.
    retry: false,
  })
}

export function useDeleteObject() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (key: string) => api.deleteStorageObject(projectId, key),
    onSuccess: () => qc.invalidateQueries({ queryKey: storageKeys.objectsAll(projectId) }),
  })
}

export function useUploadObject() {
  const projectId = useProjectId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ file, key }: { file: File; key?: string }) =>
      api.uploadStorageObject(projectId, file, key),
    onSuccess: () => qc.invalidateQueries({ queryKey: storageKeys.objectsAll(projectId) }),
  })
}
