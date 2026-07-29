# piper

Piper is a single-binary, worker-based runner for ML jobs, notebooks, and model
services.

It is for teams that need more than cron, shell scripts, or CI jobs, but do not
want to operate a full ML platform such as Kubeflow. Piper runs as a standalone
server or embedded Go library, and can dispatch work to local processes,
bare-metal workers, or Kubernetes workers.

The K8s worker mode is designed for on-prem, private-network, and air-gapped
environments: the worker runs inside the cluster and opens one outbound gRPC
tunnel to the Piper server, so the server does not need kubeconfig access or
inbound network access to the cluster.

## Intended Use

Piper is built for teams that need a small control plane for ML jobs,
notebooks, and model services without operating a full ML platform.

It provides a reliable way to:

- run DAG-based training and batch jobs
- route work to a local process, bare-metal worker, or K8s cluster
- pass artifacts between steps through local storage or S3-compatible storage
- capture logs, metrics, params, and run history
- launch notebooks close to the compute
- deploy a trained artifact as a model service
- operate across private networks without putting every kubeconfig on one server

Piper keeps these workflows in one process instead of requiring separate tools
for workflow execution, artifact passing, notebooks, serving, and cluster
access.

## Boundaries

Piper is intentionally not a full Kubeflow replacement. It does not try to be a
feature store, distributed training runtime, Kubernetes scheduler, or heavyweight
model governance platform. For large organizations that need complex approval
flows, deep experiment tracking, custom resource schedulers, or full model
registry workflows, Piper is better used as a lightweight execution layer than
as the entire ML platform.

## Features

- **DAG-based execution** — declare step dependencies with `depends_on`, automatic parallelization
- **Three deployment modes** — self-contained local / bare-metal workers / Kubernetes workers
- **Single binary** — UI included, works out of the box with `go install`
- **Embeddable library** — mount into your existing Go app and HTTP router
- **S3 artifact passing** — share files between steps via any S3-compatible store (AWS S3, SeaweedFS, etc.)
- **Real-time logs** — SSE streaming, visible in the web UI
- **Notebook and model serving workflows** — run JupyterLab servers and deploy model artifacts
- **Private-network friendly** — K8s workers connect outbound to the server for isolated clusters

## Quick Start

### Run from this repository

```bash
make build

# Reads ./config/piper.yaml and starts the API, UI, and embedded workers.
./bin/piper server
```

Open `http://localhost:8080` to use the UI. Stop the server with `Ctrl+C`.
The first browser visit creates the initial administrator.

On first start Piper generates an authentication signing key and a
credential-encryption key in `./piper-data/.server-secrets.yaml` with owner-only
permissions. It reuses that file on restart; back it up with `piper.db`.

The `server` command itself has no operational flags. Address, TLS, embedded
workers, concurrency, and all other runtime settings come from config or
`PIPER_*` environment variables.

### Install with Go

```bash
go install github.com/piper/piper/cmd/piper@latest

# Run a pipeline as a self-contained local process
piper run examples/basics/simple.yaml

# Or install the binary and provide your own ./config/piper.yaml:
piper server

# Submit to the running server
curl -X POST http://localhost:8080/api/projects/default/runs \
  -H 'Content-Type: application/json' \
  -d "{\"yaml\": $(jq -Rs . < examples/basics/simple.yaml)}"
```

Explicit `server.auth_signing_key` and `server.secret_encryption_key` values from
the config file or environment override generated keys.

## Pipeline YAML

```yaml
apiVersion: piper/v1
kind: Pipeline

metadata:
  name: my-pipeline

spec:
  # Optional: default driver settings applied to all steps.
  # Use placement to pin the whole run to one worker.
  defaults:
    driver:
      placement:
        worker: ""                # worker ID (persisted in worker.state_dir)
        label: ""                 # route to any worker registered with this label
        runtime: k8s              # baremetal | docker | k8s
      docker:
        image: alpine:3.20        # default image for Docker runtime
      k8s:
        image: alpine:3.20        # default image for K8s runtime
        namespace: piper-jobs     # required; must be in the worker allowlist

  steps:
  - name: extract
    run:
      type: command
      command: ["sh", "-c", "echo data > $PIPER_OUTPUT_DIR/data.txt"]
    outputs:
      - name: raw
        path: data.txt

  - name: transform
    depends_on: [extract]
    options:
      env:
        - name: PYTHONPATH
          value: /app
    driver:
      placement:
        label: gpu                # route to workers with this label
        runtime: k8s
      k8s:
        image: python:3.12-slim   # image for this step (overrides defaults.driver.k8s.image)
        resources:
          cpu: "4"
          memory: "16Gi"
          gpu: "1"
        pod_template:
          spec:
            nodeSelector:
              accelerator: nvidia-tesla-a100
            tolerations:
              - key: nvidia.com/gpu
                operator: Exists
                effect: NoSchedule
    run:
      type: command
      command: ["python3", "train.py"]
    inputs:
      - name: raw
        from: extract/raw         # artifact from the extract step

  - name: report
    depends_on: [transform]
    run:
      type: command
      command: ["echo", "done"]
```

### Injected Environment Variables

| Variable | Description |
|----------|-------------|
| `PIPER_OUTPUT_DIR` | Output directory for this step |
| `PIPER_INPUT_DIR` | Root of previous steps' outputs |
| `PIPER_RUN_ID` | Current run ID |
| `PIPER_STEP_NAME` | Current step name |

### Artifact Layout

Artifact storage is configured separately from execution mode.
The storage backend is selected by `storage.url`, independently of execution mode.

```text
# storage.url empty → local filesystem
{outputDir}/{runID}/{stepName}/{artifactName}/...

# storage.url uses s3:// → S3-compatible store
s3://{bucket}/{runID}/{stepName}/{artifactName}/...
```

Workers on different hosts and Kubernetes workloads require a shared remote
`storage.url` because they cannot rely on the master's local filesystem. The
self-contained local mode can use local or remote storage.

For Kubernetes, `deploy/k8s/storage-secret.example.yaml` and
`deploy/k8s/server.yaml` wire `storage.url` onto the master via
`PIPER_STORAGE_URL` — `storage.url` is read once there and forwarded to
workers per-task over the existing tunnel, so workers never need their own
copy of it. If you don't already have an S3-compatible bucket, apply
`deploy/k8s/seaweedfs.yaml` to deploy a bundled SeaweedFS S3 gateway that the
example secret points at by default.

## Execution Modes

| | Local | Bare-metal Worker | K8s Worker |
|---|---|---|---|
| **Step runs as** | host subprocess through an embedded worker | subprocess or Docker container | K8s Job (one Pod per step) |
| **Task delivery** | loopback gRPC through the production queue | gRPC push (worker → server outbound) | gRPC push (worker → server outbound) |
| **Worker location** | same process/host | any machine | inside the K8s cluster |
| **Server needs kubeconfig** | — | no | no — worker owns cluster access |
| **Artifact storage** | local or remote | local when co-located, otherwise remote | remote object storage |
| **Cancellation** | context + subprocess termination | SIGTERM / docker stop | K8s Job deletion |
| **Multi-cluster** | — | — | yes (one Kubernetes worker per cluster) |
| **Best for** | development, libraries | on-prem GPU servers | Kubernetes environments |

### 1. Local (self-contained)

`piper run` and `p.Run` start a temporary loopback master and embedded worker,
then use the same queue, tunnel, and execution path as a deployed system.

```go
p, _ := piper.New(piper.Config{OutputDir: "./outputs"})
result, _ := p.Run(ctx, yamlBytes)
```

### 2. Bare-metal Worker

Server and workers run separately. Workers connect to the master via gRPC and receive tasks by push.

```bash
# Terminal 1: API and worker tunnel share one endpoint
piper server

# Terminal 2: worker reads infrastructure and capability from its config
piper worker --config ./config/pipeline-worker.yaml
```

The worker config sets `worker.master_url`, exactly one infrastructure
(`baremetal`, `docker`, or `k8s`), and its capabilities. See the commented
worker reference in `./config/piper.yaml`.

Workers register with the master on connect. Active workers can be listed via `GET /api/workers`.

To pin an entire pipeline run to one bare-metal worker, set `spec.defaults.driver.placement.worker`.
To route all steps to workers with a specific label, set `spec.defaults.driver.placement.label`.
Individual steps can override placement via `step.driver.placement`.

### 3. Kubernetes Worker

A `piper worker` configured with `worker.k8s` runs **inside the cluster** and
connects outbound to the Piper master's unified HTTP/gRPC endpoint. The master
never needs kubeconfig access or inbound cluster access.

```
piper-server (:8080 HTTP + gRPC) ←── outbound HTTP/2 tunnel ── piper worker
                                                              │
                                                       K8s API server
                                                  (Jobs / StatefulSets / PVCs)
```

Each pipeline step runs as a K8s Job. An `initContainer` copies the `piper` binary into the step container — no changes to user images required:

```
initContainer (piper image)  →  cp /piper /piper-tools/piper
step container (user image)  →  /piper-tools/piper agent exec --task-file=/piper-task/task.json ...
```

> **vs Argo Workflows**: Argo manages workflows as K8s CRDs. Piper uses K8s only as a compute backend — DAG, retry, and state live in the Piper server. Piper runs identically in local and bare-metal modes with no K8s dependency.

**Deploy the server and worker in the cluster:**

```bash
./deploy/k8s/install.sh
```

The deployment mounts `deploy/k8s/configmap.yaml` into both processes. The
server runs `piper server`; the Kubernetes worker runs the same `piper worker`
command as every other worker type.

The `piper-worker-state` PVC stores the generated worker identity across Pod
replacement. Workload image, namespace, resources, and PVC size/class remain in
the submitted manifests.

For **multi-cluster**, deploy one worker per cluster with a different
`worker.k8s.cluster` value.
Pipeline placement is run-level: one pipeline run executes on one selected worker or cluster.

## Agent API

Workers register automatically over gRPC when they connect to the master's agent server.

```
GET  /api/workers                    list connected workers
```

Pipeline workers connect to the master through the outbound gRPC tunnel on the unified server endpoint. HTTP worker polling endpoints are not supported.

## Model Serving

Deploy a trained model as a long-running inference service using `ModelService`.
Any runtime that accepts `image + command` is supported — Triton, vLLM, TorchServe, BentoML, etc.

### ModelService YAML

```yaml
apiVersion: piper/v1
kind: ModelService

metadata:
  name: fraud-detector

spec:
  # Which artifact to serve
  model:
    from_artifact:
      pipeline: train-fraud   # Pipeline metadata.name
      step: train             # step name
      artifact: model         # outputs[].name
      run: latest             # latest | <run-id>

  # run: what to execute (command + port)
  run:
    command: ["tritonserver", "--model-repository=$(PIPER_MODEL_DIR)"]
    port: 8000
    health_path: /v2/health/ready   # optional readiness check path

  # driver: where and how to run it
  driver:
    placement:
      runtime: k8s              # baremetal | docker | k8s
      worker: ""                # pin to a specific worker (optional)
    k8s:                        # K8s runtime settings
      image: nvcr.io/nvidia/tritonserver:24.01-py3
      namespace: default
      replicas: 1
      image_pull_policy: IfNotPresent
      resources:
        cpu: "2"
        memory: "8Gi"
        gpu: "1"
    process:                    # baremetal runtime settings
      gpus: "0"                 # CUDA_VISIBLE_DEVICES
```

### Injected Environment Variables

| Variable | Description |
|----------|-------------|
| `PIPER_MODEL_DIR` | Path to the downloaded artifact (local mode) or S3 URI (k8s mode) |
| `PIPER_SERVICE_NAME` | ModelService name |

### Runtime Examples

```yaml
# Triton (K8s)
run:
  command: ["tritonserver", "--model-repository=$(PIPER_MODEL_DIR)"]
  port: 8000
driver:
  placement:
    runtime: k8s
  k8s:
    image: nvcr.io/nvidia/tritonserver:24.01-py3
    namespace: piper-serving

# vLLM (K8s)
run:
  command: ["python", "-m", "vllm.entrypoints.openai.api_server",
            "--model", "$(PIPER_MODEL_DIR)", "--port", "8000"]
  port: 8000
driver:
  placement:
    runtime: k8s
  k8s:
    image: vllm/vllm-openai:latest
    namespace: piper-serving

# TorchServe (Docker)
run:
  command: ["torchserve", "--start", "--model-store", "$(PIPER_MODEL_DIR)", "--foreground"]
  port: 8080
driver:
  placement:
    runtime: docker
  docker:
    image: pytorch/torchserve:latest

# BentoML (Docker)
run:
  command: ["bentoml", "serve", "$(PIPER_MODEL_DIR)", "--port", "3000"]
  port: 3000
driver:
  placement:
    runtime: docker
  docker:
    image: my-bentoml-service:latest
```

### Serving Worker (bare-metal)

Deploys the serving process on a **serving worker** — a separate process running on the node where the model should be served.

**Requirements**
- The runtime binary (`tritonserver`, `python`, etc.) must be installed and in `PATH` on the worker node.

**Setup**

```bash
# 1. Set server.http_addr in ./config/piper.yaml, then start the unified endpoint
piper server

# 2. Configure worker.baremetal and worker.capabilities.serving, then start it
piper worker --config ./config/serving-worker.yaml
```

**Multi-node**: run `piper worker` on each inference node. To pin a deployment
to a specific node, set `driver.placement.worker` in the ModelService YAML:

```yaml
spec:
  run:
    command: [...]
    port: 8000
  driver:
    placement:
      worker: serving-abc123  # persisted serving worker ID
      runtime: baremetal
```

---

### Auto-deploy on Pipeline Success

```yaml
spec:
  steps:
    - name: train
      ...
  on_success:
    deploy:
      service: fraud-detector   # ModelService name
      artifact: train/model     # artifact from this run
```

### Model Serving API

```
GET    /api/projects/{project_id}/serving                    list services
POST   /api/projects/{project_id}/serving                    deploy a ModelService
GET    /api/projects/{project_id}/serving/{name}             service status
DELETE /api/projects/{project_id}/serving/{name}             stop and delete
POST   /api/projects/{project_id}/serving/{name}/restart     restart
```

### Library Usage

```go
// Deploy a service
svc, err := p.DeployService(ctx, "default", []byte(modelServiceYAML))

// Stop a service
err = p.StopService(ctx, "default", "fraud-detector")

// Restart with latest artifact
err = p.RestartService(ctx, "default", "fraud-detector")
```

---

## Notebook Servers

Launch JupyterLab servers through the Piper UI. Bare-metal workers support
`process` and `docker` runtimes; Kubernetes notebooks run through a K8s worker.

### Bare-metal Worker (process or Docker)

JupyterLab is launched as a subprocess on a dedicated **notebook worker** node.

**Requirements**: JupyterLab must be installed on each notebook worker node.

```bash
# Install JupyterLab (one-time, on each worker node)
pip install jupyterlab

# Set server.http_addr in ./config/piper.yaml, then start the unified endpoint
piper server

# Configure worker.baremetal and worker.capabilities.notebook, then start it
piper worker --config ./config/notebook-worker.yaml
```

**Single-node / dev** — embed everything in one process:

```bash
piper server
```

Bare-metal and Kubernetes notebook workers both use the unified worker tunnel.

Kubernetes notebook execution is always delegated through the worker tunnel. The
server does not connect directly to the Kubernetes API.

### K8s Worker

The notebook lifecycle is managed by a `piper worker` with `worker.k8s`
configuration running inside the cluster.
Use this when the piper server cannot reach the cluster API server directly (firewall, VPN, multi-cluster).

No Kubernetes workload settings are required on the server.

Deploy it as described in the [Kubernetes Worker](#3-kubernetes-worker) section above.

### Notebook Server YAML

```yaml
apiVersion: piper/v1
kind: Notebook
metadata:
  name: my-notebook
spec:
  volume:
    size: 20Gi              # PVC size (K8s mode only)
    storage_class: standard # optional; omitted means cluster default

  driver:
    placement:
      runtime: k8s          # baremetal | docker | k8s
      worker: notebook-abc123 # persisted worker ID (optional)
    k8s:                    # K8s-specific (runtime=k8s)
      image: jupyter/scipy-notebook:latest
      namespace: piper-notebooks
      resources:
        cpu: "2"
        memory: "8Gi"
        gpu: "1"
      pod_template:
        spec:
          nodeSelector:
            accelerator: nvidia
```

For Docker notebook workers, select `runtime: docker` and provide
`driver.docker.image` plus any CPU/memory/container options in this manifest.
Process workers use `runtime: baremetal` and `driver.process`.

---

## Library Usage

```go
import piper "github.com/piper/piper"

p, err := piper.New(piper.Config{
    DBPath:    "./piper.db",
    OutputDir: "./outputs",
    Server:    piper.ServerConfig{Addr: ":8080"},
    Auth:      piper.AuthConfig{Trusted: true},
})

// Run through a temporary loopback master and embedded worker
result, err := p.Run(ctx, yamlBytes)

// Start HTTP API and the gRPC worker tunnel on the same endpoint
err = p.Serve(ctx, piper.ServeOption{})

// Mount into an existing server. Cancel ctx when the parent server shuts down.
mux.Handle("/piper/", http.StripPrefix("/piper", p.HandlerContext(ctx, nil)))

// Mount UI separately (opt-in)
import "github.com/piper/piper/pkg/ui"
mux.Handle("/piper/ui/", http.StripPrefix("/piper/ui", ui.Handler()))

// Deploy a ModelService
svc, err := p.DeployService(ctx, "default", []byte(modelServiceYAML))
```

## Configuration (`./config/piper.yaml`)

```yaml
version: 4

server:
  http_addr: ":8080"
  # Optional. When omitted, Piper generates both keys in
  # server.data_dir/.server-secrets.yaml and reuses them after restart.
  auth_signing_key: ""
  secret_encryption_key: ""
  tls:
    enabled: false
    cert_file: ""
    key_file: ""
  data_dir: ./piper-outputs
  serving:
    model_dir: ./piper-models

storage:
  url: s3://piper-artifacts?endpoint=http://localhost:9000&s3ForcePathStyle=true
  token: ""                       # prefer PIPER_STORAGE_TOKEN for secrets

worker:
  master_url: http://localhost:8080
  state_dir: ./piper-worker-state
  labels:
    region: seoul
  k8s:
    cluster: local
    namespaces: [piper-jobs, piper-notebooks, piper-serving]
    in_cluster: true
    pipeline_runner:
      image: piper/piper:latest
    notebook_volume_browser:
      image: piper/piper:latest
  capabilities:
    pipeline: {}
    notebook: {}
    serving: {}
```

`worker` describes one worker process. It must contain exactly one of
`baremetal`, `docker`, or `k8s`. Bare-metal and Docker workers must enable
exactly one capability because each corresponding command starts one role.
The K8s worker may enable multiple capabilities while managing its configured cluster.

```yaml
# Bare-metal pipeline worker
version: 4
worker:
  master_url: http://localhost:8080
  state_dir: ./piper-worker-state
  baremetal: {}
  capabilities:
    pipeline:
      label: trainer
      concurrency: 4
```

```yaml
# Docker notebook worker
version: 4
worker:
  master_url: http://localhost:8080
  state_dir: ./piper-worker-state
  docker:
    network: bridge
    volumes: []
  capabilities:
    notebook:
      notebooks_root: ./notebooks
      port_range: 8888-9900
```

### Configuration ownership

- Pipeline, Notebook, and ModelService manifests own workload image, namespace,
  resources, pod template, and notebook PVC size/class.
- `server.*` contains control-plane settings only: listener, TLS, database,
  retention, scheduling, and local data directories.
- `server.local` starts bare-metal development workers only. Docker and K8s
  workers run as standalone processes configured through `worker.*`.
- `worker.*` describes exactly one standalone worker and owns tunnel identity,
  infrastructure access, advertised capabilities, capacity, and local paths.
- `worker.k8s.namespaces` is an allowlist. It never supplies a workload default.
- Workers and workload runtimes may connect directly to `storage.url`; all other
  worker control-plane traffic uses the single master tunnel.

An explicitly selected `--config` file overrides the default project-relative
path. Environment variables override file values, which override operational
defaults. Piper never searches the user home directory. Worker Config never
overrides submitted workload fields; incomplete manifests are rejected.

Command scope:

| Command | Relevant Config |
|---|---|
| `piper server` | `server`, `storage`, `source`, `log` |
| `piper run` | local data/storage/source settings plus the submitted Pipeline manifest |
| `piper worker` | exactly one worker infrastructure plus `worker.capabilities` |

On first startup, each worker role writes its generated identity metadata under
`worker.state_dir` and reuses that ID after restart.

Server configuration is documented in the checked-in project-relative file:

```bash
piper config validate --command server
piper config show --command server
```

See `./config/piper.yaml` and `./config/piper.server.yaml`. Missing production keys
are generated in `server.data_dir/.server-secrets.yaml`. Environment values
`PIPER_SERVER_AUTH_SIGNING_KEY` and
`PIPER_SERVER_SECRET_ENCRYPTION_KEY` take precedence and are recommended when a
platform already manages secrets.

## CLI

```
piper config validate --command server           validate effective config
piper server                                     start server (API + UI)
piper run <file.yaml>                            run a pipeline locally
piper parse <file.yaml>                          validate YAML without running
piper worker                                     start the configured worker
```

## Docker

```bash
# Published image, persistent data, and automatic restart
docker compose -f deploy/docker-compose.yaml up -d

# Follow startup and inspect health
docker compose -f deploy/docker-compose.yaml logs -f piper
curl http://localhost:8080/health
```

The named volume preserves SQLite, artifacts, and
`.server-secrets.yaml`. For an explicit one-container equivalent:

```bash
docker volume create piper-data
docker run --name piper --restart unless-stopped -p 8080:8080 \
  -v ./deploy/docker/piper.yaml:/etc/piper/piper.yaml:ro \
  -v piper-data:/var/lib/piper \
  ghcr.io/loykin/piper:latest \
  --config=/etc/piper/piper.yaml server
```

To build locally, run `make docker` and set
`PIPER_IMAGE=piper/piper:latest` for Compose.

## Kubernetes

The checked-in Kustomize deployment creates the server, persistent volumes,
RBAC, and the outbound-tunnel Kubernetes worker. The installer generates the
server Secret once and preserves it on later applies:

```bash
./deploy/k8s/install.sh
kubectl -n piper port-forward service/piper-server 8080:8080
```

See [`deploy/k8s/README.md`](deploy/k8s/README.md) for storage, TLS exposure,
custom images, backup requirements, and manual secret management.

The same image is used as the server, K8s Job init container, and K8s worker:

```
server:         piper server --config ...
init container: cp /piper /piper-tools/piper
step container: /piper-tools/piper agent exec ...
worker:         piper worker --config ...
```

## Building

```bash
# Requirements
go 1.26+
node 20+   # only needed to rebuild the UI

# Full build (UI + binary)
make build

# Rebuild UI and commit dist
make ui
git add pkg/ui/dist/
git commit -m "chore: update ui dist"

# Docker image
make docker

# Tests
make test                # unit tests
make test-notebook-conformance
make test-e2e            # in-process e2e (no external infra)
make test-docker-notebook-e2e
make test-k8s-e2e        # real K8s e2e, including Kubernetes worker mode
make test-integration    # requires a running Kubernetes cluster

# Optional Docker notebook e2e. Image must already exist locally.
docker pull jupyter/minimal-notebook:latest
make test-docker-notebook-e2e
```

## Project Layout

```
cmd/piper/                      CLI entry point and cobra commands
piper.go, config.go             library entry point, import "github.com/piper/piper"

pkg/manifest/                   shared YAML types: TypeMeta, ObjectMeta, DriverSpec, SpecOptions
pkg/pipeline/                   Pipeline spec, DAG, parser, local runner
pkg/pipeline/worker/            standalone pipeline worker (baremetal/docker runtime)
pkg/pipeline/worker/driver/     Driver interface, ExecSpec, ResultOutbox, paths
pkg/pipeline/worker/driver/baremetal/  subprocess driver
pkg/pipeline/worker/driver/docker/     Docker container driver
pkg/pipeline/worker/driver/k8s/        K8s Job driver
pkg/pipeline/worker/agent/      agent exec (step entrypoint), runner, task encoding

pkg/notebook/                   Notebook spec and lifecycle
pkg/notebook/worker/            bare-metal notebook worker
pkg/notebook/worker/driver/k8s/ K8s StatefulSet notebook worker

pkg/serving/                    ModelService spec and lifecycle
pkg/serving/worker/             bare-metal serving worker
pkg/serving/worker/driver/k8s/  K8s Deployment serving worker

pkg/template/                   PipelineTemplate (submit → S3 snapshot → deploy/run)
pkg/storage/                    artifact store (S3, local, HTTP, memory)
pkg/notebook/dispatch/          master-side notebook dispatch
pkg/serving/dispatch/           master-side serving dispatch
internal/k8sworker/             unified Kubernetes worker
internal/k8sworker/pipeline/    K8s pipeline dispatch
pkg/pipeline/worker/driver/k8slauncher/ Kubernetes Job launcher
pkg/pipeline/executor/          step executors

internal/proto/                 Task, TaskResult wire types
internal/event/                 in-process pub/sub event bus
internal/logstore/              log and metric storage
internal/agent/                 server-side worker registry, routing, RPC model
internal/grpcagent/             gRPC bidirectional streaming transport
internal/queue/                 DAG task queue (retry, lease, idempotency)
internal/store/                 state persistence (SQLite / PostgreSQL)

pkg/pipeline/run/               Run and Step records
pkg/schedule/                   cron/once scheduled pipeline execution
pkg/ui/                         embedded React SPA
examples/                       usage examples
```

## Examples

| Example | Description |
|---------|-------------|
| `examples/basics/` | Sequential, parallel, and failure-propagation pipelines |
| `examples/artifacts/` | S3 artifact passing between steps |
| `examples/git-source/` | Fetching scripts from a Git repository |
| `examples/notebook/` | Jupyter notebook execution via papermill |
| `examples/notebook-template/` | Notebook workspace snapshot, `prepare`, notebook-to-Python artifacts |
| `examples/bare-metal/` | Server + worker setup with label routing (Go + YAML) |
| `examples/kubernetes/` | Per-step container images on Kubernetes |
| `examples/serving/` | ModelService deployment and inference proxy |
| `examples/library/` | In-process execution via `p.Run()` |
| `examples/embedded/` | Mounting piper into an existing HTTP server |
| `examples/mlops/` | Full MLOps demo: SeaweedFS + cron + train + auto-deploy |
