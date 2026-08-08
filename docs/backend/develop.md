# Piper — Backend Agent Guide

## Worker Network Invariant

- Workers establish one outbound tunnel to the Piper master using `worker.master_url`.
- The master serves the HTTP API and the gRPC worker tunnel on the same `server.http_addr` endpoint.
- Do not add a separate agent listener/address or reintroduce `server.agent_addr`, `worker.agent_addr`, or `--agent-addr`.
- Worker configuration describes exactly one process. Under `worker`, exactly one
  infrastructure key (`baremetal`, `docker`, or `k8s`) is allowed. Capability
  configuration always belongs in the sibling `worker.capabilities` map.
- All worker control-plane traffic must use the existing tunnel: registration, heartbeat, dispatch, cancellation, status, results, logs, metrics, notebook/serving control, and reverse proxy traffic.
- Pipeline subprocesses, containers, and Kubernetes Job pods must not call the master directly. They report locally to their parent worker, which forwards data through the tunnel.
- Artifact storage is the only exception: workers and workload runtimes may connect directly to the configured `storage.url` such as S3.
- Changes that create any additional worker-side outbound endpoint require an explicit architecture decision and documentation update.

## State Ownership — Master Is Authoritative

- All run/step state is owned by the master and persisted to SQLite/Postgres
  through `pkg/pipeline/run` (`Repository`, `StepRepository`, implemented in
  `internal/store/sqlite` and `internal/store/postgres`). Workers never write
  to these repositories directly.
- `internal/queue/queue.go`'s `Queue` is the single in-memory scheduler.
  `transitionTaskLocked` is the only point through which a task/step status
  changes; `finalizeRunLocked` is the only point through which a run reaches
  a terminal status, using a compare-and-swap (`FinalizeStatusCAS`) so the DB
  row — not the in-memory map — is the real source of truth for "is this run
  done."
- Workers report results over the tunnel
  (`internal/agent/rpcmethods.go` `MethodPipelineTaskResult` →
  `worker_push.go`'s push handler → `Queue.Complete`). The master overwrites
  `result.WorkerID` with the authenticated tunnel connection's agent ID
  (`worker_push.go`), so a worker cannot claim another worker's identity.
  `queue.go`'s `completeLocked` then rejects the report outright if it
  doesn't come from the worker the master actually assigned the task to, and
  ignores duplicate/stale/future-attempt reports. A worker's "done" report is
  a request the master validates and applies — the master decides retries,
  timeouts, and crash recovery, not the worker.
- Non-tunnel dispatch (the Kubernetes Job launcher polling loop in
  `piper.go`'s `reconcileBackend`) funnels through the exact same
  `Queue.Complete` validation path — there is no second, looser path to
  mutate state.

## Worker Assignment

- Design invariant (`pkg/manifest/driver.go` `PlacementSpec`): **one run
  executes on one worker** — every step in a run is dispatched to the same
  worker agent, enforced structurally by reusing the run's bound worker for
  every step, not just documented.
- Explicit `placement.worker` / `placement.label` / `placement.runtime` is
  **not required** when exactly one registered worker matches the run's
  runtime/label/infrastructure filters — that single candidate is picked
  implicitly.
- Explicit disambiguation **is mandatory** once more than one worker
  matches: `internal/agent/router.go` returns a non-retryable
  `AmbiguousInfrastructureError` ("N candidate workers match and none was
  named; set placement.worker to disambiguate", or the infrastructure-type
  equivalent asking for `placement.runtime`). This is treated as a
  configuration problem, not a transient capacity issue, so it is not
  retried.
- This rule is enforced at **dispatch time** in the router, not at YAML
  parse/validate time — `Pipeline.Validate()` / `Notebook.Validate()` /
  `ModelService.Validate()` do not require placement fields to be set.
- Zero matching candidates is a distinct, retryable error ("no agent
  available for placement") — do not conflate it with the ambiguous case.

## Manifest Rules

"Manifest" refers to the shared YAML kind envelope that `Pipeline`,
`Notebook`, and `ModelService` all embed — not a single unrelated concept:

- `pkg/manifest.TypeMeta` (`apiVersion`, `kind`) and `ObjectMeta` (`name`,
  `version`, `project_id`, `labels`, `description`, `tags`) are embedded by
  `pkg/pipeline.Pipeline`, `pkg/notebook.Notebook`, and
  `pkg/serving.ModelService`. **`kind`/`apiVersion` are parsed but not
  validated as a schema discriminator anywhere** — don't assume dispatch
  logic branches on them without adding that check first.
- Shared `pkg/manifest.DriverSpec` (placement + per-runtime `k8s`/`docker`/
  `process` sub-specs) and `ResourceSpec` (cpu/memory/gpu) back
  `spec.driver` on all three kinds.
- Fields actually enforced by each kind's `Validate()`:
  - Pipeline: `metadata.name` required; ≥1 step; unique step names;
    `depends_on` must reference known steps; each `driver.placement.runtime`
    must be `""`/`baremetal`/`docker`/`k8s`; `docker` requires
    `driver.docker.image`; `k8s` requires `driver.k8s.image` and
    `driver.k8s.namespace`.
  - Notebook: same runtime/image/namespace rules, plus `k8s` additionally
    requires `spec.volume.size`. An empty runtime is explicitly allowed as
    "API-level auto-assignment mode."
  - ModelService: same runtime/image/namespace rules as Pipeline.
- `pkg/manifest/k8s` is a **separate, unrelated** helper package — just
  label/annotation constants (`LabelManagedBy`, `LabelWorkloadID`,
  `AnnotationRunID`, etc.) used to stamp and later select the real
  Kubernetes objects Piper creates. It is not a schema and has no
  `Validate()` — do not confuse it with the YAML manifest kinds above.
- The local variable named `manifest` in
  `internal/agent/podpolicy_apply.go` is unrelated to both of the above —
  it's a generic round-trip of a Kubernetes `PodTemplateSpec` used to merge
  a server-side pod policy into a step's `driver.k8s.pod_template` (the
  step's own `pod_template` always takes precedence over the policy).
