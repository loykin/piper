import type { ReactNode } from 'react'
import { DataBodyTemplate, DetailBodyTemplate } from '@loykin/designkit'
import { titleCase } from '@/lib/copy'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { resourceState, type QueryLike } from '@/shared/queryState'

/**
 * The full-page counterpart of PanelPlaceholder, for detail and edit pages
 * whose resource query has no data: loading, failed (with retry), or not
 * found. Keeps the page's breadcrumbs so the user can navigate away.
 */
export function PageState({ query, noun, topBar, template = 'detail' }: {
  query: QueryLike
  noun: string
  topBar: ReactNode
  template?: 'detail' | 'data'
}) {
  const state = resourceState(query)
  const title = state === 'loading' ? 'Loading…' : state === 'failed' ? `Failed to Load ${titleCase(noun)}` : `${titleCase(noun)} Not Found`
  const body = state === 'failed'
    ? <QueryErrorNotice message={`Failed to load ${noun}`} error={query.error} onRetry={query.refetch && (() => void query.refetch?.())} />
    : <p className="text-sm text-muted-foreground">{state === 'loading' ? 'Loading…' : `This ${noun} doesn't exist or has been deleted.`}</p>

  if (template === 'data') {
    return (
      <DataBodyTemplate topBar={topBar} title={title}>
        <DataBodyTemplate.Group layout="stacked">{body}</DataBodyTemplate.Group>
      </DataBodyTemplate>
    )
  }
  return (
    <DetailBodyTemplate topBar={topBar} title={title}>
      <DetailBodyTemplate.Section>{body}</DetailBodyTemplate.Section>
    </DetailBodyTemplate>
  )
}
