# Piper

Piper is a lightweight ML pipeline orchestrator for jobs, notebooks, and model
services. It ships as one Go binary with an embedded web UI and uses outbound
worker connections, so the control plane does not need direct access to worker
hosts or Kubernetes API servers.

![Piper pipeline run history](docs/assets/piper-history.jpg)

Piper is intended for teams that have outgrown cron jobs and CI scripts but do
not need a full platform such as Kubeflow.

## What Piper provides

- DAG-based pipelines with dependencies, retries, and parallel steps
- Local, bare-metal, Docker, and Kubernetes execution
- Pipeline templates, versioned snapshots, schedules, and run history
- Logs, metrics, parameters, experiments, and artifacts
- Jupyter notebook lifecycle management
- Long-running model service deployment
- Local or S3-compatible artifact storage
- Project membership, local users, and credential management
- One outbound HTTP/2 worker tunnel for registration, dispatch, logs, and status
- An embeddable Go API

Piper is not a feature store, distributed training framework, Kubernetes
scheduler, or full model-governance platform.

## Quick start

### Build and run from this repository

Requirements:

- Go 1.26 or newer
- Node.js 24
- pnpm

```bash
pnpm install
make build
./bin/piper server
```

Open <http://localhost:8080>. On the first visit, Piper asks you to create the
initial system administrator.

The checked-in [`config/piper.yaml`](config/piper.yaml) enables embedded
pipeline, notebook, and serving workers for single-node use. The server:

- reads `./config/piper.yaml`
- listens on `:8080`
- stores SQLite data and local artifacts under `./piper-data`
- generates authentication and credential-encryption keys in
  `./piper-data/.server-secrets.yaml`
- reuses those keys on subsequent starts

Stop the server with `Ctrl+C`. Back up `piper.db` and
`.server-secrets.yaml` together.

### Run a pipeline directly

`piper run` starts a temporary loopback server and embedded pipeline worker. It
uses the same queue, tunnel, and execution path as a deployed server.

```bash
./bin/piper parse examples/basics/simple.yaml
./bin/piper run examples/basics/simple.yaml
```

### Install with Go

```bash
go install github.com/loykin/piper/cmd/piper@latest
```

`piper run` works without a configuration file:

```yaml
# pipeline.yaml
apiVersion: piper/v1
kind: Pipeline
metadata:
  name: hello
spec:
  steps:
    - name: hello
      run:
        command: ["echo", "hello from piper"]
```

```bash
piper run pipeline.yaml
```

For a persistent server, create `./config/piper.yaml` or copy the repository
sample before running `piper server`. Piper never searches a home directory for
configuration.

## Architecture

```text
Browser / API client
        │
        ▼
Piper server (:8080)
  ├─ HTTP API and embedded UI
  ├─ scheduler, queue, and state
  ├─ artifact store
  └─ gRPC worker tunnel
           ▲
           │ one outbound connection per worker
           │
      Piper worker
        ├─ bare-metal process
        ├─ Docker container
        └─ Kubernetes Jobs, StatefulSets, and Deployments
```

The server's HTTP API and worker tunnel share `server.http_addr`. Do not expose
or configure a separate worker listener.

Pipeline subprocesses, containers, and Kubernetes workload Pods report to their
parent worker. They do not open their own control-plane connection to the
server. Artifact storage is the exception: a worker or workload may access the
configured artifact endpoint directly.

## Configuration

The default path is project-relative:

```text
./config/piper.yaml
```

Use another file explicitly when needed:

```bash
piper --config ./config/piper.server.yaml config validate --command server
piper --config ./config/piper.server.yaml config show --command server
piper --config ./config/piper.server.yaml server
```

Environment variables override file values. Names are derived from YAML paths:

```text
server.http_addr              → PIPER_SERVER_HTTP_ADDR
server.auth_signing_key       → PIPER_SERVER_AUTH_SIGNING_KEY
server.secret_encryption_key  → PIPER_SERVER_SECRET_ENCRYPTION_KEY
storage.url                   → PIPER_STORAGE_URL
```

`config show` redacts secrets. `config validate` checks the effective
configuration for a server or worker.

### Server settings

The full commented reference is
[`config/piper.yaml`](config/piper.yaml). A minimal single-node server is:

```yaml
version: 4

server:
  http_addr: ":8080"
  data_dir: ./piper-data
  db:
    driver: sqlite
    path: ./piper-data/piper.db
  local:
    enabled: true
    pipeline: true
    notebook: true
    serving: true
    concurrency: 4
```

Address, TLS, database, local workers, concurrency, retention, and scheduling
belong in configuration. The `server` command intentionally has no operational
flags.

If `server.auth_signing_key` and `server.secret_encryption_key` are omitted,
Piper generates both in `server.data_dir/.server-secrets.yaml` with owner-only
permissions. Explicit configuration or environment values take precedence.

For production:

- keep generated keys on persistent storage or inject them from a secret manager
- set `server.worker_token` when standalone workers can reach the server
- terminate TLS at Piper or a trusted ingress/reverse proxy
- back up the database, artifact storage, and encryption keys together
- use PostgreSQL and an appropriate availability design before adding server
  replicas

### Worker settings

One `worker` block describes exactly one worker process:

```yaml
version: 4

worker:
  master_url: https://piper.example.com
  worker_token: ""
  state_dir: ./piper-worker-state
  labels:
    accelerator: cpu

  baremetal: {}

  capabilities:
    pipeline:
      concurrency: 4
      output_dir: ./piper-outputs
```

Rules:

- configure exactly one infrastructure: `baremetal`, `docker`, or `k8s`
- bare-metal and Docker workers enable exactly one capability
- a Kubernetes worker may enable pipeline, notebook, and serving capabilities
- `worker.state_dir` persists the generated worker identity across restarts
- workload images, namespaces, resources, and Pod templates belong to submitted
  Pipeline, Notebook, or ModelService manifests
- `worker.k8s.namespaces` is an allowlist, not a workload default

A Docker worker runs each pipeline step by bind-mounting its own running
binary (`os.Executable()`) into the step's container and executing it there
as `piper agent exec`. That only works if the worker process itself is a
Linux binary — on a non-Linux host (macOS, Windows) run the worker inside a
Linux container, using the cross-compiled `bin/piper-arm64` /
`bin/piper-amd64` from `make build-linux-arm64` / `make build-linux-amd64`,
not the host-native `piper` binary. For example, on Apple Silicon:

```bash
make build-linux-arm64
docker run -d --name piper-docker-worker \
  --add-host host.docker.internal:host-gateway \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$PWD/bin/piper-arm64:$PWD/bin/piper-arm64:ro" \
  -v "$PWD/config:$PWD/config" \
  --entrypoint "$PWD/bin/piper-arm64" \
  alpine:3.20 --config "$PWD/config/pipeline-worker.yaml" worker
```

Running the host-native binary directly still registers with the master, but
every dispatched step fails immediately (the container exits without ever
writing a result file, since the mounted binary can't execute inside the
step's Linux container) — the error and exit code depend on the step image's
init/shell, but none of them produce a working step.

Checked-in examples:

- [`config/pipeline-worker.yaml`](config/pipeline-worker.yaml)
- [`config/notebook-worker.yaml`](config/notebook-worker.yaml)
- [`config/serving-worker.yaml`](config/serving-worker.yaml)
- [`config/k8s-worker.yaml`](config/k8s-worker.yaml)

Start a standalone worker with:

```bash
piper --config ./config/pipeline-worker.yaml config validate --command worker
piper --config ./config/pipeline-worker.yaml worker
```

### Server-owned Kubernetes pipeline runtime

Kubernetes installations can launch pipeline Jobs directly from the server,
without registering a pipeline-capable Kubernetes worker:

```yaml
runtime:
  type: k8s
  namespaces: [piper]
  in_cluster: true
  workload_url: http://piper-server.piper.svc.cluster.local:8080
  pipeline_runner:
    image: ghcr.io/loykin/piper:latest
    image_pull_policy: IfNotPresent
```

`runtime.namespaces` is an allowlist. `workload_url` is required when the
built-in file artifact store is used so Job pods can reach its private `/store`
endpoint. This mode rejects `placement.worker`, `placement.label`, and any
non-Kubernetes `placement.runtime` before creating a Job.

Notebook and serving lifecycle operations remain on the compatibility
Kubernetes worker during this staged migration.

## Execution modes

| Mode | Workload | Worker location | Server needs host/cluster access |
|---|---|---|---|
| Embedded local | Host subprocess | Server process | No |
| Bare metal | Host subprocess | Any reachable node | No |
| Docker | Container | Host running Docker | No |
| Kubernetes pipeline runtime | Job | Piper's own cluster | Yes |
| Kubernetes notebook/serving | StatefulSet or Deployment | Compatibility worker cluster | No |

Standalone workers connect outbound to `worker.master_url`. Registration,
heartbeat, dispatch, cancellation, status, results, logs, metrics, notebook and
serving control, and reverse proxy traffic all use that existing tunnel.

Use placement in a workload manifest to select execution:

```yaml
driver:
  placement:
    runtime: k8s
    worker: ""       # optional exact worker ID
    label: gpu       # optional worker label
```

Pipeline defaults may set placement for the entire run. Individual steps may
override it.

## Pipeline manifests

```yaml
apiVersion: piper/v1
kind: Pipeline

metadata:
  name: training

spec:
  defaults:
    driver:
      placement:
        runtime: k8s
        label: gpu
      k8s:
        image: python:3.12-slim
        namespace: piper

  steps:
    - name: extract
      run:
        command: ["sh", "-c", "echo data > $PIPER_OUTPUT_DIR/data.txt"]
      outputs:
        - name: raw
          path: data.txt

    - name: train
      depends_on: [extract]
      run:
        command: ["python", "train.py"]
      inputs:
        - name: raw
          from: extract/raw
      outputs:
        - name: model
          path: model
      driver:
        k8s:
          resources:
            cpu: "4"
            memory: "16Gi"
            gpu: "1"
```

Piper injects:

| Variable | Meaning |
|---|---|
| `PIPER_OUTPUT_DIR` | Current step output directory |
| `PIPER_INPUT_DIR` | Root containing materialized inputs |
| `PIPER_RUN_ID` | Current run ID |
| `PIPER_STEP_NAME` | Current step name |

More examples are available under [`examples`](examples).

## Artifact storage

Artifact storage is independent of execution mode.

With no explicit `storage.url`, Piper uses its built-in file store under
`server.data_dir`. Co-located embedded workers access it directly. Standalone
and Kubernetes workers receive the server's authenticated `/store` URL and can
transfer artifacts over HTTP.

For larger distributed or production deployments, configure an S3-compatible
backend:

```yaml
storage:
  url: "s3://piper-artifacts?region=us-east-1"
  credentialRef: artifact-store
```

Credentials can also be supplied through the storage URL, `storage.token`, or
environment variables. Avoid committing secret values.

Typical artifact keys are:

```text
{runID}/{stepName}/{artifactName}/...
```

S3 is recommended when:

- workers run on multiple hosts or clusters
- workloads need high-throughput direct artifact access
- artifacts must outlive a server volume
- multiple control-plane instances share storage

## Model serving

A ModelService can deploy an artifact produced by a pipeline:

```yaml
apiVersion: piper/v1
kind: ModelService

metadata:
  name: fraud-detector

spec:
  model:
    from_artifact:
      pipeline: training
      step: train
      artifact: model
      run: latest

  run:
    command:
      - tritonserver
      - --model-repository=$PIPER_MODEL_DIR
    port: 8000
    health_path: /v2/health/ready

  driver:
    placement:
      runtime: k8s
      label: gpu
    k8s:
      image: nvcr.io/nvidia/tritonserver:24.01-py3
      namespace: piper
      replicas: 1
      resources:
        cpu: "2"
        memory: "8Gi"
        gpu: "1"
```

Piper expands `$PIPER_MODEL_DIR`, `${PIPER_MODEL_DIR}`, and
`$(PIPER_MODEL_DIR)` in command arguments.

For stored pipeline artifacts:

- a local serving process receives a local model directory
- a bare-metal or Docker serving worker downloads the artifact before launch
- a Kubernetes serving worker uses an init container and mounts the model at
  `/piper-model`

### External model URIs

Use `from_uri` for a model that is not a Piper pipeline artifact:

```yaml
spec:
  model:
    from_uri: s3://models/fraud-detector/v12
```

All current server deployments use the worker tunnel, including embedded
single-node serving:

| URI | Behavior |
|---|---|
| `file:///models/v12` | Rejected because a worker must not assume access to a server-local path |
| `s3://bucket/key` | Passed as a remotely accessible URI |
| `http://...` or `https://...` | Passed as a remotely accessible URI |

Use `from_artifact` for artifacts managed by Piper. It works with the built-in
store as well as S3 because Piper gives the worker an accessible artifact
endpoint and the worker materializes the model before launch. Do not use a
scheme-less local path in a portable ModelService manifest.

Piper also injects `PIPER_SERVICE_NAME` into serving workloads.

### Deploy automatically after a successful run

```yaml
spec:
  steps:
    - name: train
      # ...
      outputs:
        - name: model
          path: model
  on_success:
    deploy:
      service: fraud-detector
      artifact: train/model
```

The ModelService must already exist so Piper can reuse its stored deployment
manifest.

## Notebooks

Notebook manifests select the same worker infrastructure model:

```yaml
apiVersion: piper/v1
kind: Notebook

metadata:
  name: research

spec:
  volume:
    size: 20Gi
    storage_class: standard

  driver:
    placement:
      runtime: k8s
    k8s:
      image: jupyter/scipy-notebook:latest
      namespace: piper
      resources:
        cpu: "2"
        memory: "8Gi"
        gpu: "1"
```

Bare-metal notebook workers require JupyterLab on the worker host. Docker
workers use `driver.docker.image`. Kubernetes notebook lifecycle operations are
performed by the Kubernetes worker through the existing tunnel; the server does
not need kubeconfig access.

## Docker deployment

The Compose deployment runs a persistent single-node server with embedded
workers:

```bash
docker compose -f deploy/docker-compose.yaml up -d
docker compose -f deploy/docker-compose.yaml logs -f piper
curl http://localhost:8080/health
```

The `piper-data` volume preserves SQLite data, local artifacts, notebooks, and
generated server keys.

Use a locally built image:

```bash
make docker
PIPER_IMAGE=piper/piper:latest \
  docker compose -f deploy/docker-compose.yaml up -d
```

Equivalent one-container deployment:

```bash
docker volume create piper-data
docker run --name piper --restart unless-stopped -p 8080:8080 \
  -v ./deploy/docker/piper.yaml:/etc/piper/piper.yaml:ro \
  -v piper-data:/var/lib/piper \
  ghcr.io/loykin/piper:latest \
  --config=/etc/piper/piper.yaml server
```

## Kubernetes deployment

The checked-in Kustomize deployment creates:

- one Piper server that launches and reconciles pipeline Jobs directly
- one compatibility Kubernetes worker for notebook and serving
- RBAC, Services, and persistent volumes
- a generated ConfigMap sourced from
  [`deploy/k8s/piper.yaml`](deploy/k8s/piper.yaml)

Install it with:

```bash
./deploy/k8s/install.sh
kubectl -n piper port-forward service/piper-server 8080:8080
```

The installer creates `piper-server-secrets` once and preserves it on later
runs. It contains the authentication signing key, credential-encryption key,
and shared worker token.

The base deployment uses the built-in artifact store. For production, configure
an S3-compatible store with
[`deploy/k8s/storage-secret.example.yaml`](deploy/k8s/storage-secret.example.yaml).
[`deploy/k8s/seaweedfs.yaml`](deploy/k8s/seaweedfs.yaml) provides an optional
development S3 gateway.

The included Service is cluster-internal. Expose it through a TLS-enabled
Ingress or load balancer. See
[`deploy/k8s/README.md`](deploy/k8s/README.md) for image pinning, secrets,
storage, and backup guidance.

For local cluster validation with Docker and kind:

```bash
make docker
kind load docker-image piper/piper:latest
```

Before installing, change the server and worker images in `server.yaml` and
`k8s-worker.yaml`, the pipeline runner and notebook volume-browser images in
`piper.yaml`, and their pull policies to use the image loaded into kind. Then
run `./deploy/k8s/install.sh`. The same immutable image should be used for the
server, worker, pipeline init container, serving artifact fetcher, and notebook
volume browser.

## CLI

```text
piper config validate --command server  Validate effective server configuration
piper config show --command server      Print redacted effective configuration
piper parse pipeline.yaml               Validate a pipeline manifest
piper run pipeline.yaml                 Run a pipeline locally
piper server                            Start the API, UI, and worker tunnel
piper worker                            Start the configured worker
piper user                              Manage local users
```

The configuration file is the only global operational option:

```text
--config string   config file (default: ./config/piper.yaml)
```

## HTTP API

Project-scoped endpoints use `/api/projects/{project_id}`:

```text
POST   /api/projects/{project_id}/runs
GET    /api/projects/{project_id}/runs
GET    /api/projects/{project_id}/runs/{run_id}

GET    /api/projects/{project_id}/serving
POST   /api/projects/{project_id}/serving
GET    /api/projects/{project_id}/serving/{name}
DELETE /api/projects/{project_id}/serving/{name}
POST   /api/projects/{project_id}/serving/{name}/restart

GET    /api/workers
GET    /health
```

See [`docs/openapi.yaml`](docs/openapi.yaml) for the API definition.

## Embedding Piper in Go

```go
package main

import (
    "context"

    piper "github.com/loykin/piper"
)

func main() {
    ctx := context.Background()

    p, err := piper.New(piper.Config{
        DBPath:    "./piper.db",
        OutputDir: "./outputs",
        Server:    piper.ServerConfig{Addr: ":8080"},
        Auth:      piper.AuthConfig{Trusted: true},
    })
    if err != nil {
        panic(err)
    }
    defer p.Close()

    _ = ctx
}
```

Key APIs:

```go
result, err := p.Run(ctx, pipelineYAML)
err = p.Serve(ctx, piper.ServeOption{})
handler := p.HandlerContext(ctx, nil)
service, err := p.DeployService(ctx, "default", modelServiceYAML)
err = p.StopService(ctx, "default", service.Name)
```

The UI can be mounted separately with `github.com/loykin/piper/pkg/ui`.

## Development

```bash
# Build the UI and binary
pnpm install
make build

# Unit tests
make test

# Hermetic in-process E2E tests
make test-e2e

# Frontend E2E tests
make test-frontend-e2e

# Runtime-specific tests
make test-notebook-conformance
make test-docker-notebook-e2e
make test-k8s-e2e
```

`make test-k8s-e2e` requires a Kubernetes cluster and an accessible Piper image.
The Docker notebook E2E requires its configured notebook image locally.

## Repository layout

```text
cmd/piper/                 CLI commands and configuration loader
frontend/                  React web application
internal/agent/            Worker registry and RPC routing
internal/grpcagent/        Bidirectional worker tunnel
internal/k8sworker/        Kubernetes lifecycle components and compatibility worker
internal/pipelinedispatch/ Queue execution backends, including direct Kubernetes
internal/queue/            Dispatch, retry, lease, and idempotency
internal/store/            SQLite and PostgreSQL repositories
pkg/pipeline/              Pipeline parsing and execution
pkg/notebook/              Notebook lifecycle and workers
pkg/serving/               Model service lifecycle and workers
pkg/storage/               Local, HTTP, and S3-compatible artifact storage
pkg/template/              Versioned pipeline templates
pkg/ui/                    Embedded production UI
config/                    Commented server and worker examples
deploy/                    Docker Compose and Kubernetes manifests
examples/                  Runnable workload examples
```
