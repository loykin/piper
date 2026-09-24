import { PanelTemplate } from '@loykin/designkit'
import { useSidePanel } from '@loykin/side-panel'
import { X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { titleCase } from '@/lib/copy'
import { QueryErrorNotice } from '@/shared/components/QueryErrorNotice'
import { resourceState, type QueryLike } from '@/shared/queryState'

/** The detail panel's close (X) button. */
export function PanelCloseButton() {
  const { close } = useSidePanel()
  return (
    <Button variant="ghost" size="icon-sm" onClick={() => void close()}>
      <X />
      <span className="sr-only">Close</span>
    </Button>
  )
}

/**
 * What an id-based detail panel renders when its query has no data: loading,
 * failed (with retry), or gone (deleted elsewhere, or a stale link). Pass the
 * query the panel reads — for a panel that finds its item in a list query,
 * pass that list query.
 */
export function PanelPlaceholder({ query, noun }: { query: QueryLike; noun: string }) {
  const state = resourceState(query)
  const title = state === 'loading' ? 'Loading…' : state === 'failed' ? `Failed to Load ${titleCase(noun)}` : 'Not Found'
  return (
    <PanelTemplate title={title} actions={<PanelCloseButton />}>
      <PanelTemplate.Section>
        {state === 'failed' ? (
          <QueryErrorNotice message={`Failed to load ${noun}`} error={query.error} onRetry={query.refetch && (() => void query.refetch?.())} />
        ) : (
          <p className="text-xs text-muted-foreground">
            {state === 'loading' ? 'Loading…' : `This ${noun} no longer exists.`}
          </p>
        )}
      </PanelTemplate.Section>
    </PanelTemplate>
  )
}
