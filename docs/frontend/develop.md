@../../../designkit/docs/consumer-guide.md

---

# Piper — Frontend Agent Guide

## Stack

- React 19, TypeScript, Vite
- Tailwind CSS v4, `@loykin/designkit`, `@loykin/gridkit`
- React Query (`@tanstack/react-query`) for all server state
- TanStack Router (`@tanstack/react-router`)
- React Hook Form + Zod for validated forms

## Directory Layout

```
frontend/src/
  App.tsx                  — router, sidebar, auth gate
  main.tsx                 — entry point
  lib/
    api.ts                 — HTTP clients: api (system), projectApi (project-scoped)
    utils.ts               — cn()
    format.ts              — fmtDate/fmtTime/fmtDay/fmtBytes/errorMessage (never hand-roll these)
  features/
    <domain>/
      api.ts               — raw fetch functions (no React)
      hooks.ts             — React Query hooks (useQuery / useMutation)
      types.ts             — TypeScript interfaces
      columns.tsx          — DataGrid column definitions (list pages only)
      components/          — complex forms and sub-views for this domain
    projects/context.tsx   — ProjectProvider, useProjectId(), useProjectContext()
  pages/                   — one file per route, thin composition layer
  components/
    ui/                    — shadcn primitives (Button, Input, Badge, …)
  shared/
    components/            — domain-free UI (StatusBadge, QueryErrorNotice)
    hooks/                 — cross-feature hooks (useDeleteTarget, useConfirmAction, useAutoSelectSole)
    status.ts              — status → color tones (see Color Policy)
```

Dependencies point one way: `lib/`, `components/`, `shared/` → `features/` →
`pages/`. A lower layer never imports a higher one — UI that needs a
feature's hooks or types (a DAG of run steps, an env-var editor backed by
credentials) belongs in that feature's `components/`, not in `shared/`.
Between features, keep edges acyclic (`schedules` → `pipelines`, never the
reverse). `eslint.config.js` enforces these with `no-restricted-imports`.

## API Clients (`lib/api.ts`)

Two clients, both handle 401 refresh automatically:

```ts
// project-scoped — always pass projectId
import { projectApi } from '@/lib/api'
const data = await projectApi(projectId).get<T>('/notebooks')
projectApi(projectId).post('/notebooks', body)
projectApi(projectId).delete(`/notebooks/${name}`)

// system-scoped — no project prefix
import { api } from '@/lib/api'
const settings = await api.get<SystemSettings>('/api/system/settings')
```

Never construct `/api/projects/${id}/...` URLs by hand — use `projectApi`.

### Types vs. the OpenAPI spec

`lib/openapi.gen.ts` is generated from `docs/openapi.yaml` — never edit it;
run `pnpm gen:api` after changing the spec. Feature `types.ts` stay
hand-written (the spec marks few fields required, so generated types would be
looser), but `src/openapi.contract.ts` makes `tsc` fail when a feature type
has a field the spec doesn't document. When you add a response type that has a
spec schema, add one `Check<Documented<…>>` line there.

## Current Project ID

```ts
import { useProjectId } from '@/features/projects/context'
const projectId = useProjectId()
```

System pages (Users) have no project — `useProjectId()` returns `''` there.

## Feature Hooks Convention

```ts
// Query keys always include projectId for project-scoped data
export const runKeys = {
  all:  (pid: string)              => ['runs', pid] as const,
  list: (pid: string, f?: Filter)  => ['runs', pid, 'list', f] as const,
  one:  (pid: string, id: string)  => ['runs', pid, id] as const,
}

// Hooks read projectId internally
export function useRuns(filter?: Filter) {
  const projectId = useProjectId()
  return useQuery({
    queryKey: runKeys.list(projectId, filter),
    queryFn:  () => api.listRuns(projectId, filter),
    enabled:  !!projectId,
  })
}
```

System-scoped hooks (system settings, users) use `api` directly and do not include `projectId` in the query key.

### Invalidating on Mutation Success

Always invalidate through a dedicated, genuinely-shorter key (`keys.all(pid)`
above), never by calling the specific-query key builder with a missing
optional argument:

```ts
// ✗ wrong — objects(pid) with no prefix arg produces ['storage', pid, 'objects', undefined].
// React Query's invalidateQueries does prefix matching element-by-element, and
// undefined !== '' — so this silently fails to match the live query key
// ['storage', pid, 'objects', ''] used by useStorageObjectsPaged(..., '').
onSuccess: () => qc.invalidateQueries({ queryKey: storageKeys.objects(projectId) })

// ✓ correct — a key with no trailing optional param is a true prefix of every variant
onSuccess: () => qc.invalidateQueries({ queryKey: storageKeys.objectsAll(projectId) })
```

If a mutation's `onSuccess` invalidates a list query that takes an optional
filter/prefix/cursor argument, the key builder it invalidates through must not
take that argument at all — add a separate `xAll(pid)` builder rather than
calling `x(pid)` and relying on the trailing `undefined` to partial-match.

## Routing

- Navigate with `@/lib/router` (`useNavigate`, `useParams`, `useSearchParams`,
  `Link`) — string paths, one API. Only `App.tsx` (the route tree) and
  `lib/router.tsx` import `@tanstack/react-router`; ESLint enforces this.
- Create pages live at `…/new` (`/users/new`, `/projects/:project_id/schedules/new`),
  edit/sub-actions at `…/:id/<verb>` (`credentials/:name/rotate`,
  `pipelines/:id/deploy`). When renaming a route, keep the old path as a
  redirect (`RenamedRouteRedirect` in `App.tsx`) so bookmarks keep working.
- Every page sets `topBar={<PageTopBar left={<PageCrumbs items={…} />} />}`:
  the sidebar section, then ancestors as `{ label, to }` links, then the
  current page. Use `shared/components/PageCrumbs` rather than DesignKit's
  `PageBreadcrumb` — the latter renders plain `href`s that ignore the `/ui`
  router basepath.

```
/projects/:project_id/*   — project routes (most pages)
/users                    — system user list
/users/new                — system user creation form
/login                    — auth
```

Project routes are children of `projectRoute` in `App.tsx`. System routes are
children of `appLayoutRoute` and must not require a project ID.

## UI Components — No Raw HTML, No Custom CSS

**Never use raw HTML elements where a component exists:**

```tsx
// ✗ wrong
<button className="rounded-md bg-primary px-4 py-2 text-sm ...">Submit</button>

// ✓ correct
<Button size="sm">Submit</Button>
```

Always reach for these first:
- `Button` from `@/components/ui/button` — all clickable actions
- `Input`, `Label`, `Badge`, `Switch` from `@/components/ui/` — form controls and display
- `IconButton` from `@/components/ui/icon-button` — icon-only actions
- DesignKit components (`DataBodyTemplate`, `DetailBodyTemplate`, etc.) — page structure

**Never write inline Tailwind to replicate a component's appearance.** If a variant or size is missing, add it to the component — don't work around it with one-off classes.

## Color Policy

- **Page chrome is fixed**: shells, toolbars, tables, pagination, form groups,
  panels, control sizes, spacing, loading/error/empty states, and action
  placement all come from DesignKit/`components/ui`. If a variant is missing,
  add it there.
- **Semantic status colors live in one place**: `src/shared/status.ts`. Map a
  backend status with `statusTone(status)` and use its class sets
  (`toneBadge`, `toneText`, `toneFill`, `toneOutline`, `toneAction`,
  `warningNotice`). Every tone defines a light and a dark shade together.
- **Domain views (DAG, log viewer, YAML/shell editors, charts) may shape
  themselves freely**, but colors still come from semantic theme tokens
  (`bg-card`, `text-muted-foreground`, `border-border`, …) or the tones above.
- A raw palette class (`text-red-400`, `bg-green-500/20`, …) anywhere outside
  `src/shared/status.ts` is an ESLint error (`no-restricted-syntax` in
  `eslint.config.js`).

## CSS Setup

Styles must be imported in `index.css` via `@import`, not in `main.tsx` via JS `import`. Libraries using `@layer` (e.g. gridkit) must be `@import`ed at the very end of `index.css` so their layer priority stays above Tailwind's `@layer base`.

```css
/* index.css — correct order */
@import "tailwindcss";
@import "@loykin/designkit/styles";   /* after tailwindcss */
@import "tw-animate-css";
/* ... other CSS ... */
@import "@loykin/gridkit/styles";     /* last — uses @layer gridkit */
```

## Page Conventions

- Pages are thin: fetch via hooks, compose with DesignKit template + DataGrid.
- No business logic in pages — keep it in `features/<domain>/`.
- Each page is lazy-loaded in `App.tsx` (see Route Loading — `lazyRouteComponent`).
- **Use `DataBodyTemplate`** for data/list/settings pages (see DesignKit section above).

### Resource List Interaction Pattern

Resource management pages must use the same interaction model unless a
documented domain constraint requires an exception:

1. The list route renders `DataBodyTemplate` with a `DataGrid`.
2. The primary create action is placed in `DataBodyTemplate.Resource`'s
   `toolbarRight` (the DesignKit managed-table contract) and
   navigates to a dedicated route such as `/users/new` or
   `/projects/:project_id/credentials/new`.
3. Do not place a create/edit form permanently above the list.
4. Do not put a create/edit form in a `Dialog`, `AlertDialog`, or SidePanel.
5. Clicking a data row opens a detail SidePanel using
   `SidePanelProvider`, `useSidePanel()`, and DesignKit `PanelTemplate`.
6. Detail panels belong in `features/<domain>/components/`, not inline in the
   page. They may show metadata and quick actions but are not full create/edit
   pages.
7. Set `rowCursor` when `onRowClick` is present. Interactive controls inside a
   row must call `event.stopPropagation()` so they do not also open the detail
   panel.
8. Use `AlertDialog` only for explicit confirmation of destructive or
   irreversible actions. Back the "which row/action is pending confirmation"
   state with `shared/hooks/useDeleteTarget.ts` (delete flows) or
   `useConfirmAction.ts` (multi-verb flows like cancel/delete on a detail
   view) instead of a hand-rolled `useState`. Always wire the dialog's
   `onOpenChange` as `open => { if (!open) cancel() }` — forwarding an
   `onOpenChange(true)` straight into state is what let one dialog reopen
   itself against a different target right after a successful action. A
   single-candidate `Select`/dropdown that would otherwise need a manual
   choice should use `useAutoSelectSole.ts` instead. For the common
   title/description/Cancel/confirm shape, render
   `shared/components/ConfirmDialog` instead of hand-assembling the
   `AlertDialog*` parts; ESLint rejects passing a state setter straight to an
   `AlertDialog`'s `onOpenChange`.
9. Surface a failed list query with `shared/components/QueryErrorNotice.tsx`
   in `DataBodyTemplate.Resource`'s `notice` prop — `message`, `error`, and
   `onRetry={() => void query.refetch()}`. Don't hand-roll an error `<p>`;
   every list page's query can fail and the retry affordance is part of the
   contract, not an optional extra.
   **The `notice` prop itself must evaluate to a falsy value (not an empty
   `<>...</>` Fragment) when there is nothing to show.** `Resource` renders a
   spacer container whenever `notice` is truthy, even if that Fragment's own
   children are all `false` — an empty Fragment is still a truthy React
   element. Passing `notice={cond && <X/>}` for a single condition is safe;
   for two conditions, gate the whole Fragment on their combined truthiness
   instead of just wrapping both in a bare Fragment:
   ```tsx
   // ✗ wrong — <>...</> is truthy even when both children render false,
   // so Resource reserves ~12px of empty space below the toolbar on every
   // page load, not just when there's actually a notice to show
   notice={
     <>
       {query.isError && <QueryErrorNotice ... />}
       {actionError && <p>{actionError}</p>}
     </>
   }

   // ✓ correct — notice is false outright when neither condition applies
   notice={(query.isError || actionError) && (
     <>
       {query.isError && <QueryErrorNotice ... />}
       {actionError && <p>{actionError}</p>}
     </>
   )}
   ```
10. Row action columns render icon-only controls — `RowActions` wrapping
    `IconButton`s (the `label` prop supplies a11y text and a tooltip, never
    visible text). Never render an action as a `Button`/anchor with both an
    icon and visible text sitting next to icon-only siblings — pick one style
    for the whole row and it's icon-only. This also applies to a link-style
    action (e.g. an external "Open" link): style it as an icon-only anchor at
    the same `size-7`/`icon-sm` dimensions, not a full labeled button.

11. Every list page's `toolbarLeft` opens with a text search box built from
    `@loykin/filter-input`'s `FilterInput` (`type: 'text'`), not a raw
    `<Input>` — the raw `<Input>` search box that predates this rule had no
    real precedent anywhere else in the app; `FilterInput` is the sanctioned
    component now. Wrap it in a fixed-width `<div>` (`w-48`/`w-52`) since it
    fills its container:
    ```tsx
    const [nameFilter, setNameFilter] = useState('')
    const filtered = useTextFilter(query.data?.items, nameFilter, item => item.name)

    <DataBodyTemplate.Resource
      toolbarLeft={
        <div className="w-48">
          <FilterInput
            config={{ key: 'itemSearch', type: 'text', placeholder: 'Search items…' }}
            value={nameFilter}
            onChange={v => setNameFilter(typeof v === 'string' ? v : '')}
          />
        </div>
      }
    >
    ```
    On a paginated list, this only filters the current page — the same
    accepted trade-off as `CredentialsPage`'s `kind` filter (documented
    inline there) — since these endpoints don't support server-side
    substring search yet. Pass `useTextFilter` the query's own array
    (`query.data?.items`), not a `?? []`-derived local — the latter is a fresh
    array every render and defeats memoization.
    `FilterInput`'s own `inputClassName`/`classNames.control` cannot reliably
    override the package's base `.fi-control` styles (padding, height, etc.)
    due to CSS import-order — see
    `/Users/loykin/Project/basekit/packages/filter-input/ISSUES.md` for the
    full writeup. Don't fight this with utility class overrides.

Rule 5 has no standing exception for "the row only has a few columns" or
"there's nothing more to show than the grid already displays" — a column
that's truncated or `flex: 1`-squeezed off-screen (an object key, a long ID)
is exactly the case a detail panel exists to fix, and even a read-only
resource (no create/edit form) still benefits from one for that reason. Before
skipping rule 5, check whether the omission is because there's truly nothing
to show, or because the page just didn't get a panel built yet.

Reference implementations:

- List + SidePanel: `pages/pipelines/PipelinesListPage.tsx`,
  `pages/notebooks/NotebooksPage.tsx`, `pages/system/UsersPage.tsx`,
  `pages/credentials/CredentialsPage.tsx`, `pages/notebooks/NotebookVolumesPage.tsx`,
  `pages/serving/ServingHistoryPage.tsx`, `pages/system/StoragePage.tsx`
  (Uploaded Objects)
- Dedicated create page: `pages/credentials/CredentialCreatePage.tsx`,
  `pages/system/UserCreatePage.tsx`
- Detail panel: `features/pipelines/components/PipelineDetailPanel.tsx`,
  `features/access/components/UserDetailPanel.tsx`,
  `features/credentials/components/CredentialDetailPanel.tsx`,
  `features/storage/components/ObjectDetailPanel.tsx` (copy-to-clipboard for
  a long technical value)

### Tabs (`DataBodyTemplate.Tab`)

A page with multiple independent resource areas uses `DataBodyTemplate.Tab`,
one per area, as direct children of the `DataBodyTemplate` root — never
nested inside `Body`, `Group`, or another layout mode. Reference:
`pages/system/StoragePage.tsx` (Configuration / Objects tabs).

- **Bind the active tab to a URL search param**, not bare `useState`. Left
  uncontrolled, `DataBodyTemplate` defaults to internal state that resets to
  the first tab on every reload — a real regression a user will hit by
  refreshing the page.
  ```tsx
  const [searchParams, setSearchParams] = useSearchParams()
  const activeTab = searchParams.get('tab') ?? DEFAULT_TAB

  function handleTabChange(next: string) {
    setSearchParams({ ...Object.fromEntries(searchParams), tab: next }, { replace: true })
  }

  <DataBodyTemplate activeTab={activeTab} onTabChange={handleTabChange} ...>
    <DataBodyTemplate.Tab id="config" label="Configuration">...</DataBodyTemplate.Tab>
    <DataBodyTemplate.Tab id="objects" label="Objects">...</DataBodyTemplate.Tab>
  </DataBodyTemplate>
  ```
- **A tab whose content is a list/grid still follows the Resource List
  Interaction Pattern above** — render `DataBodyTemplate.Resource` (toolbar,
  `notice`, pagination footer) as that tab's direct child, exactly as it would
  appear under `Body` on a non-tabbed list page. Do not substitute
  `DataBodyTemplate.Group` for a tab's list content — `Group` is for
  form-workflow save boundaries (Form Convention below), and using it for a
  list silently drops the managed-table toolbar/notice/pagination contract
  the other list pages follow, producing a table with a different structure
  than every other list in the app.
- A tab whose content is a settings form still follows the Form Convention
  below (`DataBodyTemplate.Group layout="stacked"`).

### Form Convention

- Validated create/edit forms with multiple fields use React Hook Form with a
  Zod schema and `zodResolver`.
- Render controls from `@/components/ui/` or public DesignKit exports.
- The form lives on a routed page composed with `DataBodyTemplate`.
- Follow the DesignKit playground's **Form Stacked** composition: render a
  direct `DataBodyTemplate.Group layout="stacked"` child with a title and
  description, put the form inside the group, use `space-y-3` field spacing,
  and place compact Cancel/Submit buttons at the form's bottom-right.
- Do not wrap a standard stacked form in `DataBodyTemplate.Body`, constrain it
  with an arbitrary one-off width, or move its submit button into page actions.
- Keep API submission in a feature mutation hook. The page handles navigation
  and user-visible submission errors.

### Users and Project Members

- A User is a system identity. Its built-in login identifier is `username`;
  do not invent profile fields that the backend cannot persist.
- `system_admin` is the only global privilege. It grants system administration
  and implicit access to every project.
- `viewer`, `member`, and `admin` are project-specific roles stored on Project
  Memberships, not on User accounts.
- The Users detail SidePanel must show the account's project memberships so
  global and project access are visibly connected.
- The Project Members page manages memberships for the current project.
  Add members by exact username; never require users to copy an opaque user ID.

## DataGrid (list pages)

```ts
import { DataGrid, DataGridPaginationBar, type DataGridColumnDef } from '@loykin/gridkit'
```

Column definitions live in `features/<domain>/columns.tsx`.
Columns passed to `<DataGrid>` should be memoized with `useMemo` when they capture callbacks.

### Pagination

Every list page uses **server-side pagination with `DataGridPaginationBar`** —
this is the DesignKit `managed-table` contract's only sanctioned pagination
component; `DataGridPaginationCompact` client-side paging is not an
alternative, even for small collections. `pages/pipelines/HistoryPage.tsx` /
`features/runs/{api,hooks}.ts` (`useRunsPaged`) is the reference
implementation — copy its shape for a new list page:

- API layer: a `listXPaged(projectId, limit, offset)` function that returns
  `projectApi(projectId).getPaged<T>(...)` (or `api.getPaged` for
  system-scoped lists) — a `Paged<T>` (`{ items, total }`, from `lib/api.ts`).
  Never invent a per-feature envelope key (`{ users }`, `{ rules }`, …); every
  paged list is `Paged<T>` and pages read `query.data?.items`. `total` comes
  from the `X-Total-Count` response header, which the server only sets when a
  `limit` query param was sent — see `internal/httpx.SetTotalCountHeader` on
  the Go side — so a paged call must always send `limit`.
- Unpaginated lists use `getList<T>(...)`, cursor lists `getCursorList<T>(...)`.
  All three throw `ContractError` when the body isn't a JSON array (or the
  total header is missing) — don't wrap them in `Array.isArray(data) ? data :
  []`. That guard turned a backend regression into a silently empty list; the
  backend's side of the contract (never `null`) is enforced by
  `list_contract_test.go`.
- Hook layer: a `useXPaged(limit, offset)` query with
  `placeholderData: (prev) => prev` so the grid doesn't flash empty between
  pages.
- Page layer:
  ```tsx
  const [pageIndex, setPageIndex] = useState(0)
  const query = useXPaged(PAGE_SIZE, pageIndex * PAGE_SIZE)
  const total = query.data?.total ?? 0

  <DataGrid
    classNames={{ footer: 'pt-3' }}
    pagination={{
      pageSize: PAGE_SIZE,
      pageIndex,
      pageCount: Math.max(1, Math.ceil(total / PAGE_SIZE)),
      onPageChange: setPageIndex,
    }}
    footer={(table) => <DataGridPaginationBar table={table} totalCount={total} />}
  />
  ```

Keep the existing unbounded `useX()`/`listX()` hook and API function too when
other call sites need the full, un-paginated list (e.g. populating a lookup
map or a picker dropdown) — don't force those callers through the paginated
variant. Add `Offset`/`Count` to the Go repository interface and both SQLite
and Postgres implementations (see `pkg/pipeline/run` for the reference
shape); don't add pagination to only one store backend.

## Loading State

Never hide entire page content behind a single loading flag. Render sections independently so each can show its own loading state:

```tsx
// ✗ wrong — flickers on refetch, blocks all sections
const loading = l1 || l2 || l3
{loading ? <Spinner /> : <AllContent />}

// ✓ correct — sections render independently
<Section isLoading={l1} ... />
<Section isLoading={l2} ... />
```

## Route Loading

- Use TanStack Router's `lazyRouteComponent`, not `React.lazy`, for routed
  pages so the router can preload route modules.
- Configure intent preloading and preload imperative sidebar destinations on
  pointer/focus intent.
- Keep the application shell and sidebar outside the page-level `Suspense`
  boundary. A lazy page must never replace the entire application with a
  loading message.
- Delay the page fallback briefly so cached or fast route modules do not flash
  a transient loading state.
- During authentication bootstrap, render a stable content skeleton instead of
  returning `null`.
- Apply the saved/system theme in `index.html` before the first paint. Do not
  force a temporary theme in `main.tsx`.

## Polling Queries (`refetchInterval`)

React Query distinguishes two loading states:

| Property | True when | Use for |
|----------|-----------|---------|
| `isLoading` | initial fetch only (no cached data) | full skeleton |
| `isFetching` | any fetch in progress incl. refetch | subtle indicator |

**Rules for hooks that use `refetchInterval`:**

0. Poll only genuinely live status/monitoring resources. Catalog and
   configuration resources such as pipeline templates must rely on mutation
   invalidation and normal stale/refocus behavior instead of interval polling.
1. Use `backgroundPolling()` from `lib/query.ts` for fixed intervals, or
   `backgroundPollingNotifications` for state-dependent intervals. Do not
   duplicate `refetchInterval`/`notifyOnChangeProps` boilerplate in hooks.
2. Never pass `isLoading={isLoading}` to `DataGrid` on monitoring/status pages — the skeleton will flash on every poll. Omit the prop; the DataGrid shows the empty/data state immediately and updates silently.

```ts
// hooks — polling query
import { backgroundPolling } from '@/lib/query'

export function useServices() {
  const projectId = useProjectId()
  return useQuery({
    queryKey: servingKeys.list(projectId),
    queryFn: () => api.listServing(projectId),
    enabled: !!projectId,
    ...backgroundPolling(5000),
  })
}

// page — no isLoading on DataGrid for polling sections
<DataGrid
  data={data}
  columns={columns}
  // isLoading omitted — monitoring pages show empty state immediately
  emptyMessage="No services deployed."
  ...
/>
```

Without `notifyOnChangeProps`, each poll fires two re-renders (`isFetching` true → false) which propagate into DataGrid internals and cause visible flicker. React `<StrictMode>` (dev only) amplifies this by remounting components, making the skeleton re-appear on each cycle.

## Adding a New Feature

1. Create `features/<domain>/types.ts`, `api.ts`, `hooks.ts`.
2. Add `columns.tsx` if the feature has a list view with a DataGrid.
3. Add `components/` sub-directory for forms or detail views.
4. Create `pages/<Domain>Page.tsx` using the appropriate DesignKit template.
5. Register the route in `App.tsx`.
6. Export new types through the feature's `api.ts` re-export if other features need them.

## Tests

- Unit: `pnpm test` (vitest, `src/**/*.test.ts`, node environment). Test
  pure logic — YAML↔draft conversion, validation, formatters, the list
  contract in `lib/api.ts` — not component markup.
- End to end: `pnpm test:e2e` (Playwright; starts `examples/frontend-e2e` on
  :18080 and a preview build on :4173, so both ports must be free).
  `PIPER_PIPELINE_TEMPLATE_E2E_ENV` points at a Python venv for the agent.
