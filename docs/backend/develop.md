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

## State Ownership — Workers Own Scheduling, the Master Owns the Record

The master used to run a single in-memory scheduler (`internal/queue.Queue`)
that owned DAG promotion, retry, timeout, and finalization for every step of
every run. That package has been deleted. Each worker's own local scheduler
now owns those decisions for the runs bound to it; the master's job shrank
to placement, DB hosting, and being the durable source of truth those
decisions get written through.

- All run/step state is still persisted to SQLite/Postgres through
  `pkg/pipeline/run` (`Repository`, `StepRepository`, implemented in
  `internal/store/sqlite` and `internal/store/postgres`) — that part is
  unchanged. What changed is *who decides* what gets written and when.
- **Dispatch is run-level, not step-level.** `Piper.startRun` calls
  `pipelinedispatch.AgentBackend.DispatchRun` once per run, handing the
  worker the whole pipeline YAML in a single `pipeline.run_dispatch` RPC.
  Before that RPC goes out, `confirmRunBinding` persists `runs.worker_id`
  durably — this ordering matters because it's the authorization root every
  subsequent RPC from that worker is checked against (see below), and a
  fast run can otherwise report back before an unordered write would have
  landed.
- **The worker's scheduler owns everything after that.**
  `pkg/pipeline/worker/scheduler.RunScheduler` (one per run, shared by the
  baremetal/docker driver in `pkg/pipeline/worker/worker.go` and the k8s
  driver in `internal/k8sworker/pipeline/worker.go` — one engine, since
  `driver.Driver`'s `Start`/`Wait`/`Stop`/`Recover` contract already
  abstracts the infrastructure differences) owns dependency promotion,
  retry, timeout, and run finalization locally. The master's DB is
  informed, not consulted, for each of those decisions.
- **Workers report state changes as authenticated request-response RPCs**,
  not pushes: `pipeline.step_upsert` and `pipeline.run_finalize`
  (`internal/agent/rpcmethods.go`, sent via `grpcagent.Client.SendRequest`,
  handled by `registerPipelineDBHandlers` in `pipeline_db_handlers.go`).
  Each handler resolves the acting worker from the authenticated tunnel
  connection (never from the payload), checks it against the run's
  `runs.worker_id`, and applies the write with a DB-level compare-and-swap
  (`StepRepository.UpsertCAS`, `Repository.FinalizeStatusCAS`) — the DB row,
  not any in-memory map, is what a stale/duplicate/wrong-worker report gets
  rejected against.
- **Events and hooks fire from inside these handlers now**, not from a
  queue's finalize path: `registerPipelineDBHandlers` takes an
  `event.Publisher` and an `onRunSuccess` callback, and emits
  `step.<status>` on every applied `step_upsert` and `run.completed`/
  `run.canceled` on every applied `run_finalize` (invoking `onRunSuccess` —
  the `on_success.deploy` hook — for a successful run). `run_finalize` also
  calls `pipelinedispatch.RunOwner.ReleaseRun` on the backend so its
  in-memory run binding doesn't leak once a run reaches any terminal status,
  not just when it's explicitly canceled.
- **Crash recovery is now three separate, narrower mechanisms** instead of
  one queue-wide recovery pass:
  - *Worker process restart*: the worker loads its local
    `pkg/pipeline/worker/driver.RunDispatchStore` (a durable copy of every
    `pipeline.run_dispatch` it received, including the resolved credential
    `Env` the DB never persists), calls `pipeline.worker_recovery_query` to
    get back its own non-terminal runs/steps plus any `cancel_requested_at`
    that arrived while it was down, and rebuilds a `RunScheduler` per run —
    re-attaching already-running steps via `driver.Recover()`.
  - *Master restart*: `Piper.resendUndeliveredRunDispatches` (called from
    `reconcileInterruptedRuns` at startup and periodically from
    `runCleanup`) resends `pipeline.run_dispatch` for any run the DB says is
    still running. This is safe because `Registry.StartRun` is idempotent —
    a worker that already has that run just logs and ignores the resend.
  - *Permanently lost worker*: `Piper.sweepStaleWorkerBoundRuns` is the
    backstop nothing else covers, since the master no longer watches
    individual steps. A run is force-finalized (Failed, or Canceled if
    `cancel_requested_at` is set) only when **both** its run-level heartbeat
    (`pipeline.lease_renew`'s `run_ids`, pushed every 10s, touching
    `runs.worker_last_seen_at`) is stale **and** the worker is absent from
    the live connection registry — so a worker that's merely slow, or
    actively reconnecting, is never mistaken for dead.
- **Cancel ownership transfers to the worker.** `Piper.CancelRun` →
  `cancelDispatchedRun` persists `SetCancelRequested` durably *before*
  attempting a best-effort live relay (`CancelableBackend.CancelRun`) — so
  the intent survives even if the worker is unreachable right now. The
  worker's own scheduler is what actually stops the run and calls
  `run_finalize(canceled)`; the master never unilaterally decides a run's
  outcome out from under a worker that's just slow to respond.
- **`pipelinedispatch.RunDispatchBackend` is the only supported dispatch
  contract.** The old per-step `ExecutionBackend`/`Dispatch` interface, the
  in-process `LocalBackend`, and the embedded Kubernetes Job launcher
  reconciliation path (`piper.go`'s former `reconcileBackend`/
  `jobReconciler`, which depended on `Queue.Complete`) have all been
  removed along with `Queue` — `Piper.SetBackend` now requires a
  `RunDispatchBackend`, and `AgentBackend` is the only implementation.
  `StartRun`/`CancelRun` fail outright if no backend is configured.

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
