# Piper — Backend Agent Guide

HTTP handlers and clients must also follow
[`docs/backend/api-conventions.md`](api-conventions.md); the concrete contract
is [`docs/openapi.yaml`](../openapi.yaml).

## Execution Model — Direct, In-Process, No Worker Tunnel

Piper owns pipeline, notebook, and serving execution directly and in-process.
There is no remote worker, no gRPC worker tunnel, and no "server vs worker"
role split — a single Piper server is the whole installation for a given
`runtime.type`. (This is unrelated to fed.md §13.4's Home/Member tunnel,
which federates *management* across separate Piper installations — see
`internal/membertunnel` and `proto/member.proto` — not workload dispatch.)

- `runtime.type` (`k8s` | `docker` | `baremetal`) is **required** — there is
  no empty-string fallback and no other execution mode. `cmd/piper/config`'s
  `validateRuntime` rejects an empty `runtime.type` with a clear error before
  a `*Piper` is ever constructed.
- The server drives its configured Kubernetes client, Docker daemon client,
  or subprocess supervisor directly. Pipeline/Notebook/Serving dispatch,
  cancellation, observation, and recovery never leave the process boundary
  except to talk to the infrastructure itself (the K8s API, the Docker
  daemon, or a spawned subprocess).
- Artifact storage is the one legitimate outbound exception: Kubernetes Job
  pods and Docker containers may need to reach Piper's own file-backed
  artifact store over HTTP (`runtime.workload_url` /
  `runtime.docker.workload_url`), guarded by `ServerConfig.WorkloadToken` and
  `workerTokenMiddleware` on the `/store` route group — this middleware name
  is a holdover from the pre-deletion architecture but the mechanism itself
  is still real and load-bearing; don't remove it while any runtime uses the
  built-in file store.

## Server-Owned Pipeline Runtime (k8s / docker / baremetal)

- `runtime.type: k8s|docker|baremetal` moves **pipeline** Job/container/process
  lifecycle ownership into the Piper server.
- Direct runtime results still enter through `Queue.Complete`; the runtime must
  not write run/step repositories or finalize runs itself. This applies
  uniformly across `internal/pipelinedispatch`'s `K8sBackend`, `DockerBackend`,
  and `BaremetalBackend` — all three wrap a driver-agnostic
  `internal/directworker.Worker` (docker/baremetal) or
  `internal/k8sworker/pipeline.Worker` (k8s) whose `ReportResult`/`Complete`
  callback is the only path into the queue.
- For `k8s`, the configured `runtime.namespaces` list is the complete namespace
  scope for creation, recovery, and cancellation — never silently expand it
  based on a workload manifest. Docker and baremetal have no namespace concept.
- `placement.worker` and `placement.label` are invalid for all three direct
  runtimes (enforced by the shared `pipelinedispatch.validateDirectPlacement`
  helper). `placement.runtime` may be empty or match the configured
  `runtime.type`; any other value must fail before dispatch. Notebook and
  serving's direct-runtime drivers (docker/baremetal and K8s, both domains)
  enforce the exact same rule via `notebook.ValidateDirectPlacement` /
  `serving.ValidateDirectPlacement` (fed.md §13.6) — same shape as the
  pipeline helper, just simpler since Notebook/ModelService have one
  placement per workload rather than per-step.
- Storage reachability differs by runtime: Kubernetes Job pods and Docker
  containers cannot reach the host's local filesystem directly, so when
  Piper's built-in file store is used, `runtime.workload_url`
  (`runtime.docker.workload_url` for Docker) is the private HTTP endpoint base
  given to the workload instead of a raw `file://` path. Baremetal
  subprocesses share the host filesystem directly and need no such rewrite —
  `runtime.baremetal` has no `workload_url` field.
- `docker`/`baremetal` direct execution has no external scheduler bounding
  concurrent work on the Piper host (unlike `k8s`, which is bounded by the
  Kubernetes cluster scheduler) — `runtime.docker.concurrency` /
  `runtime.baremetal.concurrency` (default 4) is a required admission gate in
  `internal/directworker.Worker`, not an optional tuning knob.
- Notebook has the same `docker`/`baremetal` direct-runtime treatment as
  pipeline: `pkg/notebook/dispatch/localdriver.Driver` implements
  `notebook.Driver` directly against `pkg/notebook/worker/driver`'s
  docker/process backends, selected in `piper.go` by the same
  `cfg.Runtime.Type` switch used for pipeline dispatch. `notebook.Manager`
  never trusts `Driver.Start`'s returned status — it only persists status
  changes reported later through a callback (`localdriver.Config.ReportStatus`
  → `Manager.UpdateStatus`), so `localdriver.Driver.Start` must return fast
  with placeholder connection info and report the real outcome asynchronously
  from a background goroutine.
- Serving has the same `docker`/`baremetal` direct-runtime treatment:
  `pkg/serving/dispatch/localdriver.Driver` implements `serving.Driver`
  directly against `pkg/serving/worker/driver`'s docker/process backends,
  selected the same way in `piper.go`. Unlike Notebook, `serving.Manager.Deploy`
  is fully synchronous and *does* trust `Driver.Deploy`'s returned
  `*serving.Service.Status` immediately (it upserts it as-is) — so
  `localdriver.Driver.Deploy` itself must return fast with `status=starting`
  and only report `running`/`failed` asynchronously once a background health
  check (`process.WaitReady` against `spec.run.health_path`) resolves, with a
  `failService`/`exitAs` override so a health-check failure reports `failed`
  (not the runtime's raw exit status) once the stopped process/container's
  own exit callback eventually fires.
  `localdriver.Driver.ArtifactTarget()` returns `artifact.TargetLocal`, so
  Piper's existing artifact resolver (`service_api.go`) fully resolves the
  model to a local host path *before* `Deploy` is ever called — s3/http(s)
  downloads happen there, not in this driver. Direct-mode Docker serving
  bind-mounts that resolved directory read-only at `/piper/model` and sets
  `PIPER_MODEL_DIR` to the container path; a host path in an environment
  variable alone is not reachable from inside the container.
- Notebook additionally has a K8s direct-runtime path:
  `pkg/notebook/dispatch/localdriver/k8s.Driver` implements `notebook.Driver`
  directly against `kubernetes.Interface` (StatefulSet + PVC + headless
  Service), selected by the same `cfg.Runtime.Type` switch. It is a
  *separate* package from the docker/process `localdriver`, not a third case
  in it — K8s notebooks need a fundamentally different lifecycle (StatefulSet
  + PVC + polling-based readiness) than the docker/process backends' shared
  low-level `notebookdriver.Driver` interface models, so there is no common
  low-level type to switch on. `Start` returns fast with `status=starting`;
  the real `running`/`failed` transition is detected by a long-running
  `Observe` background goroutine (started once at `New`, independent of any
  connection lifecycle) that polls StatefulSet readiness and reports through
  the same `ReportStatus` callback shape the docker/process driver uses.
  `Endpoint` is a plain `http://<svc>.<ns>.svc.cluster.local:8888` URL (no
  `tunnel://` scheme) — this assumes Piper itself has cluster-internal network
  reachability to the target namespaces (e.g. deployed as a pod in the same
  cluster via `runtime.k8s.in_cluster`), which is already a supported
  deployment mode for `runtime.type: k8s`, not a new constraint.
  `pkg/notebook/handler.go`'s `proxyNotebook` always reverse-proxies directly
  to `Endpoint` (mirroring `pkg/serving/proxy.go`) — there is no scheme
  branching, since nothing produces a `tunnel://` endpoint anymore.
  `NotebookVolume.RuntimeID` is left empty here too (same reasoning as
  docker/process) — it can no longer be used to tell a K8s volume apart from
  a local one, since it's empty for every direct-runtime infra type now.

  Template snapshot capture (pipeline-template creation's local-source
  upload, `pkg/template/handler.go`'s `uploadSnapshot`) reads a notebook
  volume's live files through `notebook.WorkspaceReader`
  (`pkg/notebook/workspace.go`), wired per `runtime.type` in `piper.go`:
  `LocalWorkspaceReader` (plain host `os.Stat`/`os.Open`) for baremetal/
  docker, and `pkg/notebook/dispatch/localdriver/k8s.WorkspaceReader` for
  K8s — the latter execs into the volume's currently-running notebook pod
  (`remotecommand`, kubectl-exec equivalent; requires the notebook to be
  running, no stopped-notebook fallback), replacing the tunnel-based
  `volume_browser.go` capability the deleted remote K8s worker used to
  provide. The Role granting the `piper-server` ServiceAccount pod exec
  access needs `pods/exec` (`create`) — see `deploy/k8s/rbac.yaml`.

  The **separate** `GET /notebook-volumes/:id/files` volume file browser
  endpoint (`pkg/notebook/handler.go`'s `listVolumeFiles`, used by the
  frontend's local-source file picker) uses the same `WorkspaceReader`, so a
  K8s volume is listed through the running notebook pod rather than treating
  its container path as a host path. Workspace-relative paths are validated
  before either local filesystem access or pod exec, and local reads reject
  symlink escapes outside the volume.
- Serving also has a K8s direct-runtime path:
  `pkg/serving/dispatch/localdriver/k8s.Driver` implements `serving.Driver`
  directly against `kubernetes.Interface` (Deployment + Service), the same
  separate-package split as notebook's for the same reason (K8s serving needs
  a Deployment/Service lifecycle the shared low-level `servingdriver.Driver`
  interface doesn't model). Unlike docker/baremetal serving direct-runtime
  (`ArtifactTarget=TargetLocal`, model pre-resolved to a local path before
  `Deploy` runs), a K8s pod cannot reach Piper's local filesystem — this
  driver returns `artifact.TargetRemote` and, when the model comes from a
  stored artifact (`art.ArtifactKey` set), delivers it via an artifact Secret
  (`storage-url`/`storage-token`/`artifact-key`) plus an `artifact-download`
  init container (`/piper internal artifact-download`, reusing
  `cfg.Runtime.K8s.PipelineRunnerImage` as the fetcher image — same binary
  Pipeline's K8s runtime already uses for runner pods, no new config field)
  that populates an `EmptyDir` volume mounted into the serving container.
  Because Piper's own storage URL/token are only resolved partway through
  `piper.New` (after the driver is constructed), the driver exposes a
  `WithStorage(url, token string)` setter called once resolution completes.
  `Endpoint` is a plain `http://<name>.<ns>.svc.cluster.local:<port>` URL;
  `pkg/serving/proxy.go` reverse-proxies directly to whatever URL is stored,
  so no scheme-branching was ever needed on the serving side.
  `serving.Manager.Deploy` is fully synchronous and trusts the returned
  `*serving.Service.Status` as-is, same constraint the docker/baremetal
  serving driver has — `Deploy` returns fast with `status=starting`, and the
  real `running`/`failed` transition is detected by the same kind of
  long-running `Observe` background goroutine notebook's K8s driver uses.

## State Ownership — Master Is Authoritative

- All run/step state is owned by the master and persisted to SQLite/Postgres
  through `pkg/pipeline/run` (`Repository`, `StepRepository`, implemented in
  `internal/store/sqlite` and `internal/store/postgres`). Direct-runtime
  drivers never write to these repositories directly.
- `internal/queue/queue.go`'s `Queue` is the single in-memory scheduler.
  `transitionTaskLocked` is the only point through which a task/step status
  changes; `finalizeRunLocked` is the only point through which a run reaches
  a terminal status, using a compare-and-swap (`FinalizeStatusCAS`) so the DB
  row — not the in-memory map — is the real source of truth for "is this run
  done."
- Every runtime (`k8s`, `docker`, `baremetal`) reports task completion through
  the exact same `Queue.Complete` validation path — there is no second,
  looser path to mutate state. `queue.go`'s `completeLocked` rejects a report
  that doesn't come from the runtime the master actually assigned the task
  to, and ignores duplicate/stale/future-attempt reports.

## Workspace vs. Artifact Repository (fed.md §13.6)

Two distinct, independently-lifecycled things live under `OutputDir`, and
code that reads or cleans up local files must be deliberate about which one
it means:

- **Workspace** (`OutputDir/<runID>/<step>/`): the ephemeral directory a
  step's own process/container writes to while running. Tied to the run's
  lifecycle — cleaned up by `runTTL` (via `deleteRunWorkspace`, called from
  `deleteRunWithArtifacts`/`deleteRunsWithArtifacts`) or by the orphan sweep
  (`cleanupOrphanArtifacts`, which now always runs regardless of whether a
  Store is configured — it used to no-op whenever any Store existed, which
  silently leaked every run's workspace directory forever under the default
  config).
- **Artifact repository** (`p.store`, a `storage.Store` — `LocalStore`
  rooted at `OutputDir/store` by default, or S3/HTTP/cloud when configured):
  the durable, keyed (`{runID}/{step}/{artifactName}/…`) copy that
  `uploadOutputs` (`pkg/pipeline/worker/agent/runner.go`) writes after each
  step completes, for every runtime including baremetal. Cleaned up by
  `artifactTTL` (via `deleteArtifactsFromStore`) — independently of
  `runTTL`, so an expired artifact doesn't have to take the run record with
  it, and vice versa.

`piperArtifactResolver.Resolve`'s `TargetLocal` case (`resolveLocal`) is the
one place that reads a local artifact back out, and it must prefer the
repository over the workspace whenever a `Store` exists:

- `Store` is a `*storage.LocalStore`: read directly from `store.Root()` —
  same host, same disk, no copy needed.
- `Store` is remote (S3/HTTP/cloud): stage a local copy once, under
  `OutputDir/artifact-cache/…` (`artifactCacheDirName`), and reuse it on
  later resolutions instead of re-downloading. This is required, not an
  optimization: `pkg/pipeline/worker/agent.Runner` sets `cleanWorkdir=true`
  whenever the store isn't a `LocalStore` and deletes the step's workspace
  copy right after upload — so for a remote store, the workspace is *already
  gone* by the time anything resolves `TargetLocal` later (e.g. a baremetal
  or Docker `ModelService` deploying `from_artifact`, since that driver's
  `ArtifactTarget()` is unconditionally `TargetLocal`). Reading from the
  workspace in this case doesn't just risk a stale copy, it fails outright.
- No `Store` at all: only the workspace copy exists — read from there, same
  as before this distinction existed.

Because the repository can live inside `OutputDir` (the default `store`
subdirectory) and the cache directory always does, both must be excluded
from `cleanupOrphanArtifacts`'s run-ID sweep — `Piper.cleanupOrphanArtifacts`
computes the `LocalStore` exclusion dynamically via `filepath.Rel` (it isn't
always literally named `store`; a custom `storage.url` can point anywhere
under `OutputDir`), always excludes `artifactCacheDirName` and `.results`
(the fixed bookkeeping dir the baremetal/docker drivers write task/result
JSON to — see `pkg/pipeline/worker/driver/{baremetal,docker}.Start`), and
dynamically excludes `runtime.baremetal.meta_dir` when it's nested under
`OutputDir`.

**`OutputDir` is always resolved to an absolute path in `New()`**, right
after the config default is applied — a real local QA pass (fed.md §14)
found that leaving it relative (`./piper-data`, the documented default)
broke this in two ways: `filepath.Rel` against a relative base and
`storage.NewLocal`'s always-absolute `Root()` errors out, which the
exclusion logic silently swallowed and let the sweep delete the store
itself; and Docker's bind-mount source must be absolute outright, so
`runtime.type: docker` couldn't even start a container. Don't reintroduce a
relative-`OutputDir` code path anywhere downstream — rely on the
already-absolute `cfg.OutputDir` instead of re-deriving or re-validating it.

## Manifest Rules

"Manifest" refers to the shared YAML kind envelope that `Pipeline`,
`Notebook`, and `ModelService` all embed — not a single unrelated concept:

- `pkg/manifest.TypeMeta` (`apiVersion`, `kind`) and `ObjectMeta` (`name`,
  `version`, `project_id`, `labels`, `description`, `tags`) are embedded by
  `pkg/pipeline.Pipeline`, `pkg/notebook.Notebook`, and
  `pkg/serving.ModelService`. Every accepted manifest uses `apiVersion: piper/v1` and
  exactly one of `kind: Pipeline`, `kind: Notebook`, or
  `kind: ModelService`. Missing, partial, or mismatched envelopes are rejected;
  legacy envelope-less records must be migrated instead of silently accepted.
- Shared `pkg/manifest.DriverSpec` (placement + per-runtime `k8s`/`docker`/
  `process` sub-specs) and `ResourceSpec` (cpu/memory/gpu) back
  `spec.driver` on all three kinds. Canonical `PlacementSpec` contains only
  `runtime`; strict API parsing rejects the removed `worker` and `label`
  fields. Stored manifests from before this change can still carry those
  fields in their saved YAML text (Pipeline templates, Notebook records,
  ModelService records) —
  `internal/manifestmigrate` (`piper manifest migrate [--apply]`, fed.md
  §13.6) scans and cleans those up. Notebook/ModelService are rewritten in
  place; Pipeline templates get a new version with the field removed rather
  than a mutated old version, since template versions are otherwise
  immutable everywhere else in the codebase.
- Fields actually enforced by each kind's `Validate()`:
  - Pipeline: `metadata.name` required; ≥1 step; unique step names;
    `depends_on` must reference known steps; each `driver.placement.runtime`
    must be `""`/`baremetal`/`docker`/`k8s`; `docker` requires
    `driver.docker.image`; `k8s` requires `driver.k8s.image` and
    `driver.k8s.namespace`.
  - Notebook: `metadata.name` plus the same runtime/image/namespace rules,
    with `k8s` additionally
    requires `spec.volume.size`. An empty `driver.placement.runtime` is
    allowed — it means "use whatever runtime this Piper installation is
    configured with," resolved at dispatch time, not "pick among candidates"
    (there is only ever one candidate: this installation).
  - ModelService: `metadata.name`, exactly one model source, a non-empty run
    command, a valid TCP port, and the same runtime/image/namespace rules as
    Pipeline.
- All user-submitted kinds use `manifest.DecodeStrict`: unknown fields,
  multiple YAML documents, and removed placement fields are rejected.
- `pkg/manifest/k8s` is a **separate, unrelated** helper package — just
  label/annotation constants (`LabelManagedBy`, `LabelWorkloadID`,
  `AnnotationRunID`, etc.) used to stamp and later select the real
  Kubernetes objects Piper creates. It is not a schema and has no
  `Validate()` — do not confuse it with the YAML manifest kinds above.
