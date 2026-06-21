@../designkit/docs/consumer-guide.md

---

# Piper — Frontend Agent Guide

## Worker Network Invariant

- Workers establish one outbound tunnel to the Piper master using `worker.master_url`.
- The master serves the HTTP API and the gRPC worker tunnel on the same `server.http_addr` endpoint.
- Do not add a separate agent listener/address or reintroduce `server.agent_addr`, `worker.agent_addr`, or `--agent-addr`.
- Worker configuration describes exactly one process. Under `worker`, exactly one
  infrastructure key (`baremetal`, `docker`, or `k8s`) is allowed, and capability
  configuration belongs under that selected infrastructure's `capabilities` map.
- All worker control-plane traffic must use the existing tunnel: registration, heartbeat, dispatch, cancellation, status, results, logs, metrics, notebook/serving control, and reverse proxy traffic.
- Pipeline subprocesses, containers, and Kubernetes Job pods must not call the master directly. They report locally to their parent worker, which forwards data through the tunnel.
- Artifact storage is the only exception: workers and workload runtimes may connect directly to the configured `storage.url` such as S3.
- Changes that create any additional worker-side outbound endpoint require an explicit architecture decision and documentation update.

## Stack

- React 19, TypeScript, Vite
- Tailwind CSS v4, `@loykin/designkit`, `@loykin/gridkit`
- React Query (`@tanstack/react-query`) for all server state
- React Router v6

## Directory Layout

```
frontend/src/
  App.tsx                  — router, sidebar, auth gate
  main.tsx                 — entry point
  lib/
    api.ts                 — HTTP clients: api (system), projectApi (project-scoped)
    projectContext.tsx     — ProjectProvider, useProjectId(), useProjectContext()
    utils.ts               — cn()
  features/
    <domain>/
      api.ts               — raw fetch functions (no React)
      hooks.ts             — React Query hooks (useQuery / useMutation)
      types.ts             — TypeScript interfaces
      columns.tsx          — DataGrid column definitions (list pages only)
      components/          — complex forms and sub-views for this domain
  pages/                   — one file per route, thin composition layer
  components/
    ui/                    — shadcn primitives (Button, Input, Badge, …)
    ProjectSelector.tsx
  shared/
    components/            — cross-feature UI (PipelineCanvas, RunDAG, StatusBadge)
    hooks/                 — cross-feature hooks (usePolling)
```

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
const workers = await api.get<Worker[]>('/api/workers')
```

Never construct `/api/projects/${id}/...` URLs by hand — use `projectApi`.

## Current Project ID

```ts
import { useProjectId } from '@/lib/projectContext'
const projectId = useProjectId()
```

System pages (Workers) have no project — `useProjectId()` returns `''` there.

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

System-scoped hooks (workers, notebook-workers, serving-workers) use `api` directly and do not include `projectId` in the query key.

## Routing

```
/projects/:project_id/*   — project routes (most pages)
/workers                  — system route (no project)
/login                    — auth
```

Project routes are nested inside `<Routes>` under `projects/:project_id/*`.

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
- Each page is lazy-loaded via `React.lazy` in `App.tsx`.
- **Use `DataBodyTemplate`** for data/list/settings pages (see DesignKit section above).

## DataGrid (list pages)

```ts
import { DataGrid, DataGridPaginationCompact, type DataGridColumnDef } from '@loykin/gridkit'
```

Column definitions live in `features/<domain>/columns.tsx`.
Columns passed to `<DataGrid>` should be memoized with `useMemo` when they capture callbacks.

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

## Polling Queries (`refetchInterval`)

React Query distinguishes two loading states:

| Property | True when | Use for |
|----------|-----------|---------|
| `isLoading` | initial fetch only (no cached data) | full skeleton |
| `isFetching` | any fetch in progress incl. refetch | subtle indicator |

**Rules for hooks that use `refetchInterval`:**

1. Always add `notifyOnChangeProps: ['data', 'isLoading']` — prevents re-renders on every `isFetching` toggle (which happens twice per interval).
2. Never pass `isLoading={isLoading}` to `DataGrid` on monitoring/status pages — the skeleton will flash on every poll. Omit the prop; the DataGrid shows the empty/data state immediately and updates silently.

```ts
// hooks — polling query
export function useWorkers() {
  return useQuery({
    queryKey: workerKeys.list(),
    queryFn: api.listWorkers,
    refetchInterval: 5000,
    notifyOnChangeProps: ['data', 'isLoading'],  // ← required
  })
}

// page — no isLoading on DataGrid for polling sections
<DataGrid
  data={data}
  columns={columns}
  // isLoading omitted — monitoring pages show empty state immediately
  emptyMessage="No workers registered."
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
