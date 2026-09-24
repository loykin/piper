import { ApiError } from '@/lib/api'

/** The parts of a TanStack query that a loading / missing / failed view needs. */
export type QueryLike = { isLoading: boolean; error: unknown; refetch?: () => unknown }

export type ResourceState = 'loading' | 'missing' | 'failed'

/**
 * Why a single resource has no data to show. Only a 404 means it doesn't
 * exist — a 500 or a network error must not read as "not found", because
 * the user would conclude it was deleted.
 */
export function resourceState(query: QueryLike): ResourceState {
  if (query.isLoading) return 'loading'
  if (query.error && !(query.error instanceof ApiError && query.error.status === 404)) return 'failed'
  return 'missing'
}
