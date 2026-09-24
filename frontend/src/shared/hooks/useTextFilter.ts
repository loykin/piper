import { useMemo } from 'react'

/**
 * Case-insensitive substring filter for a list page's toolbar search box.
 * `items` should be the query's own array (e.g. `query.data?.items`), not a
 * `?? []` fallback, so the memo only recomputes when the data changes.
 * On a paginated list this filters the current page only — see
 * docs/frontend/develop.md rule 11.
 */
export function useTextFilter<T>(items: T[] | undefined, query: string, text: (item: T) => string | undefined): T[] {
  const needle = query.trim().toLowerCase()
  return useMemo(() => {
    const list = items ?? []
    return needle ? list.filter(item => (text(item) ?? '').toLowerCase().includes(needle)) : list
    // `text` is an inline accessor that changes identity every render; the
    // result only depends on the items and the query.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items, needle])
}
