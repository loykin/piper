# HTTP API conventions

`docs/openapi.yaml` is the machine-readable contract. New handlers and clients
must follow these rules so standalone and federated routing expose the same API.

- Project-owned resources live below `/api/projects/{project_id}`. System-owned
  resources live below `/api/system`; authentication endpoints live below
  `/api/auth`.
- Resource paths use plural nouns. Use a nested `POST /{resource}/{id}/{action}`
  only when the operation is not ordinary CRUD.
- JSON fields and query parameters use `snake_case`. Error responses use exactly
  `{"error":"message"}` unless the OpenAPI contract declares extra fields.
- `GET` returns `200`; a collection returns a JSON array and a detail endpoint
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
