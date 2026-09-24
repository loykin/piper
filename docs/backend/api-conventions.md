# HTTP API conventions

`docs/openapi.yaml` is the machine-readable contract. New handlers and clients
must follow these rules so standalone and federated routing expose the same API.

- Project-owned resources live below `/api/projects/{project_id}`. System-owned
  resources live below `/api/system`; authentication endpoints live below
  `/api/auth`.
- Resource paths use plural nouns (`/pipeline-templates`, `/storage/objects`).
  A per-project singleton setting is the one singular exception
  (`/notebook-execution-policy`). Use a nested `POST /{resource}/{id}/{action}`
  only when the operation is not ordinary CRUD.
- Once created, a resource is addressed by its own id at its own collection
  (`/notebook-executions/{id}`), even when it is created under a parent
  (`POST /notebooks/{name}/executions`). Clients must not need the parent's
  name to read or act on it. Keys that contain `/` use a catch-all path
  segment (`/storage/objects/{key}`), never a `?key=` query.
- `PATCH` changes some fields and leaves the rest; `PUT` replaces the whole
  resource or document. Pick by what the handler does, not by habit.
- JSON fields and query parameters use `snake_case`. Error responses use exactly
  `{"error":"message"}` unless the OpenAPI contract declares extra fields.
- `GET` returns `200`; a collection returns a JSON array — `[]` when empty,
  never `null` (`list_contract_test.go`) — and a detail endpoint
  returns its documented resource or detail envelope. Clients must not accept
  undocumented legacy response unions.
- A `POST` that creates one or more resources returns `201`, including action
  endpoints such as rerun, retry, backfill, and new viewer creation. An
  idempotent viewer open that returns an already-running resource returns `200`. A
  successful mutation with no response body returns `204`. `DELETE` returns
  `204` unless it starts an asynchronous operation represented by a resource.
- Retriable mutations accept `Idempotency-Key`. The key is scoped to the
  project, repeated identical requests return the original result, and reuse
  with different content returns `409 Conflict`.
- Member-owned routes must be registered on the fail-closed relayed project
  group. A remote project must never fall through to Home's local repository.
- Renaming or reshaping a published endpoint requires an explicit compatibility
  window in the OpenAPI contract and tests. Do not leave silent aliases or
  frontend-only compatibility branches behind.
- A lookup of an unknown id answers `404` — never `500`, and never `200` with
  an empty list for a sub-collection of a missing parent (`/runs/{id}/steps`).
  Only a genuine not-found is a 404: a repository `Get` returns its domain
  `ErrNotFound` for a missing row, and handlers use `httpx.LookupFailed`,
  which maps that to 404 and any other error to 500. The UI shows "Not Found"
  only for a 404, so mapping a database error to 404 tells the user the
  resource was deleted. `missing_resource_contract_test.go` walks every
  parameterized route with unknown ids.
- A resource looked up by id alone (viewers, notebook volumes) must be checked
  against the URL's project; another project's resource answers 404.
- An unexpected failure answers through `httpx.InternalError(c, err, msg)`: the
  full error is logged with a short reference id, and the response is only
  `{"error":"<msg> (ref 3f9a2c1b)"}`. Never put `err.Error()` in a 500 body —
  raw errors carry SQL, file paths, and upstream response bodies — and grep
  the server log for the ref a user reports instead.
  `internal_error_contract_test.go` fails on a 500 built from a value.
  Expected failures (404, 409, 400 validation) keep their specific message.
- Decode request bodies with `httpx.BindJSON`, whose 400 names the JSON field
  ("type is required") instead of the validator's Go-side text.
- `openapi_routes_test.go` fails when a registered JSON route is missing from
  `docs/openapi.yaml` or the spec documents a route the server no longer
  registers — update both in the same change.
