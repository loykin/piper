# Piper — Backend Agent Guide

## Single Agent-Exec Entry Point

`agent_exec.go` (repo root, `package piper`) is the **only** implementation of
`piper agent exec`. Its `init()` intercepts `os.Args == ["agent", "exec", ...]`
before `main()` runs, in any binary that imports `github.com/loykin/piper` —
including the real `piper` CLI (`cmd/piper/main.go` imports the root package).
Because Go runs every `init()` before `main()`, **a cobra subcommand or a
binary-local re-implementation of "agent exec" can never actually execute**
once its binary imports this package — it is silently dead code, not a
fallback.

This bit us for real: a cobra `agent exec` subcommand
(`cmd/piper/commands/agent.go`), `TestMain`-based copies in both the root e2e
test binary and the `examples` package's e2e test binary, and a full local
re-implementation in `examples/frontend-e2e/main.go` all existed side by side
with `agent_exec.go`, none of them reachable. When
storage-credential CLI args were removed in favor of env vars, the fix only
landed in the dead copies at first — the actually-running one
(`agent_exec.go`) kept the old flag-only behavior, and only a real e2e run
against a fake S3 backend caught it. The same dead copies were also missing
SIGTERM handling (baremetal cancel/timeout would orphan the step's child
process) and Kubernetes termination-log truncation (`/dev/termination-log`'s
4096-byte cap) that `agent_exec.go` needed but never had — both now live only
in `agent_exec.go`.

**Do not add another "agent exec" implementation.** If a binary needs to run
steps via the baremetal/docker driver pattern without importing the root
`piper` package (as `examples/bare-metal/worker/main.go` deliberately does,
to demonstrate standalone embedding), that is the one legitimate exception —
keep it, but don't let its logic drift from `agent_exec.go` without a reason
to. Everywhere else, importing `github.com/loykin/piper` is what makes "agent
exec" work at all; don't also hand-roll a second path for it.

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

## Storage Ownership Invariant

- **Location is master-authoritative**: where an artifact lives (which
  backend, which URL/prefix) is resolved by the master
  (`resolveStorageURL`/`injectStorageCredential` in `piper.go`) and handed to
  the worker per-task over the existing tunnel. Workers do not decide where
  to store or fetch artifacts on their own.
- **Authentication is worker-local, delivered through the execution
  environment's own credential mechanism — never as a CLI argument.**
  Storage credentials (`--storage-token`/`--storage-url`) must never appear
  in a subprocess or container's argv: they're visible to any host user via
  `ps aux`, to `docker inspect`, and to `kubectl describe pod`. This mirrors
  the existing rule for git/task credentials
  (`pkg/pipeline/worker/agent/exec.go`'s `TaskFile` comment: "Prefer this
  over --task so secret-bearing task env is not exposed in argv").
  `AgentExecConfig.StorageEnv()` is the single place that turns storage
  credentials into delivery-ready values; `BuildAgentExec` itself never puts
  them on the command line.
  - **baremetal**: process env var via `core.Spec.Env`
    (`pkg/pipeline/worker/driver/baremetal/driver.go`) — narrower exposure
    than argv (`/proc/PID/environ` needs same-uid access; argv is visible to
    any local user).
  - **docker**: container env via `container.Config.Env`
    (`pkg/pipeline/worker/driver/docker/driver.go`) — still visible via
    `docker inspect`, since standalone Docker (unlike k8s) has no per-task
    secret-ref primitive to attach to a single container; this is a partial
    mitigation, not parity with k8s.
  - **k8s**: `secretKeyRef`-backed env vars, never a plain `Value`
    (`pkg/pipeline/worker/driver/k8slauncher/launcher.go`'s
    `buildEnvVars`/`createTaskSecret`) — the same per-job Secret that already
    carries `task.json` also carries `storage-url`/`storage-token` keys, so
    `kubectl describe pod` shows only the secret/key reference, never the
    value. This mirrors Serving's already-correct pattern
    (`pkg/serving/worker/driver/k8s/worker.go`'s `upsertArtifactSecret`/
    `secretEnv`).
- When adding a new driver or a new secret-bearing config value, follow the
  same rule: extend `AgentExecConfig`/`StorageEnv()` (or the equivalent for
  the new value) rather than adding a new CLI flag, and thread it through the
  environment-native mechanism for that infrastructure type.

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

**This is still exactly true today, including with the DB-access RPC
interface below.** `pipeline_db_handlers.go` registers real,
authorization-checked handlers for `pipeline.step_upsert`,
`pipeline.run_finalize`, and `pipeline.worker_recovery_query` — but nothing
in production *calls* them yet (`grpcagent.Client.SendRequest` for these
methods has no caller outside tests). They exist as tested, working
scaffolding for a future worker-owned scheduler (a design direction, not yet
built), so that landing it later is "wire up a caller" rather than "invent
the DB interface and its authorization model from scratch." Until a
worker-side scheduler actually exists and calls them, `Queue` remains the
only thing deciding retries, timeouts, DAG promotion, and run finalization —
read the bullets above as the current, load-bearing contract, not as
aspirational. When that changes, this section needs to be rewritten to
match, not patched around.
- One piece of that future interface's authorization model is already live
  and worth knowing about regardless: `runs.worker_id` is now set by
  `pipelinedispatch.AgentBackend.Dispatch`, *before* the dispatch RPC is
  sent to the worker (see `confirmRunBinding`) — this exists specifically so
  the DB-access handlers above can trust it as an authorization root without
  a race where a fast run's workload starts before its binding is durable.

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
