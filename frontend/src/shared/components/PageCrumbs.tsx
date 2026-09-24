import { Link } from '@/lib/router'
import { Fragment } from 'react'
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from '@loykin/designkit'

export type PageCrumb = string | { label: string; to?: string }

/**
 * The page hierarchy for `PageTopBar`'s `left` slot. Same markup and sizing
 * as DesignKit's `PageBreadcrumb`, but ancestor links go through the router
 * (`PageBreadcrumb`'s plain `href` ignores the app's `/ui` basepath). The
 * last item is the current page and is never a link.
 */
export function PageCrumbs({ items }: { items: PageCrumb[] }) {
  return (
    <Breadcrumb>
      <BreadcrumbList className="gap-1 text-xs">
        {items.map((item, i) => {
          const label = typeof item === 'string' ? item : item.label
          const to = typeof item === 'string' ? undefined : item.to
          const isLast = i === items.length - 1
          return (
            <Fragment key={i}>
              {i > 0 && <BreadcrumbSeparator />}
              <BreadcrumbItem>
                {isLast
                  ? <BreadcrumbPage className="text-xs">{label}</BreadcrumbPage>
                  : to
                    ? <BreadcrumbLink className="text-xs" render={<Link to={to} />}>{label}</BreadcrumbLink>
                    : <span className="text-xs text-muted-foreground">{label}</span>}
              </BreadcrumbItem>
            </Fragment>
          )
        })}
      </BreadcrumbList>
    </Breadcrumb>
  )
}
