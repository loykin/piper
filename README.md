# piper

Piper is a single-binary, worker-based runner for ML jobs, notebooks, and model
services.

It is for teams that need more than cron, shell scripts, or CI jobs, but do not
want to operate a full ML platform such as Kubeflow. Piper runs as a standalone
server or embedded Go library, and can dispatch work to local processes,
bare-metal GPU workers, direct Kubernetes Jobs, or cluster-local K8s workers.

The K8s worker mode is designed for on-prem, private-network, and air-gapped
environments: the worker runs inside the cluster and opens an outbound WebSocket
tunnel to the Piper server, so the server does not need kubeconfig access or
inbound network access to the cluster.

## Intended Use

Piper is built for teams that need a small control plane for ML jobs,
notebooks, and model services without operating a full ML platform.

It provides a reliable way to:

- run DAG-based training and batch jobs
- route work to a local process, GPU worker, or K8s cluster
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
- **Four execution modes** — local (in-process) / bare-metal worker / Kubernetes Jobs / K8s Worker
- **Single binary** — UI included, works out of the box with `go install`
- **Embeddable library** — mount into your existing Go app and HTTP router
- **S3 artifact passing** — share files between steps via any S3-compatible store (AWS S3, SeaweedFS, etc.)
- **Real-time logs** — SSE streaming, visible in the web UI
- **Notebook and model serving workflows** — run JupyterLab servers and deploy model artifacts
- **Private-network friendly** — K8s workers connect outbound to the server for isolated clusters

## Quick Start

```bash
go install github.com/piper/piper/cmd/piper@latest

# Start server
piper server

# Run a pipeline (CLI)
piper run examples/simple.yaml

# Run a pipeline (API)
curl -X POST http://localhost:8080/runs \
  -H 'Content-Type: application/json' \
  -d "{\"yaml\": $(cat examples/simple.yaml | jq -Rs .)}"
```

Open `http://localhost:8080` to view the UI.

## Pipeline YAML

```yaml
apiVersion: piper/v1
kind: Pipeline

metadata:
  name: my-pipeline

spec:
  # Optional: pin the whole run to one worker or K8s cluster.
  # Step-level placement is intentionally unsupported.
  placement:
    worker: ""                 # bare-metal worker ID/hostname
    cluster: ""                # K8s worker cluster name
    namespace: ""              # K8s namespace for worker-created Jobs

  defaults:
    image: alpine:3.20        # default container image for K8s mode

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
    runner:
      label: gpu              # only run on workers with this label
      node_selector:          # K8s only: schedule on specific nodes
        accelerator: nvidia-tesla-a100
      pod_labels:             # K8s only: labels added to the step Pod
        team: ml
      scheduler_name: ""      # K8s only: custom scheduler (optional)
    run:
      type: command
      image: python:3.12-slim # override container image for this step (K8s mode)
      command: ["python3", "train.py"]
    inputs:
      - name: raw
        from: extract/raw     # artifact from the extract step

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
The storage backend is determined by whether S3 is configured — not by which execution mode is used.

```text
# S3 not configured → local filesystem
{outputDir}/{runID}/{stepName}/{artifactName}/...

# S3 configured → S3-compatible store
s3://{bucket}/{runID}/{stepName}/{artifactName}/...
```

Worker and Kubernetes modes require S3 because steps run on separate machines or Pods and cannot share a local filesystem.
In-process mode works with either backend.

## Execution Modes

| | Local | Bare-metal Worker | Kubernetes Jobs | K8s Worker |
|---|---|---|---|---|
| **Runs on** | single process | separate worker processes | K8s Jobs (one Pod per step) | K8s Jobs via cluster worker |
| **Task delivery** | direct call | HTTP polling `/api/tasks/next` | K8s Job created per step | worker RPC over WebSocket tunnel |
| **Artifact storage** | local filesystem | shared filesystem or S3 | S3 required | S3 required |
| **Cancellation** | context cancel | SSE event | `kubectl delete job` | worker RPC |
| **Multi-cluster** | — | — | single cluster only | yes (one worker per cluster) |
| **Best for** | development, libraries | on-prem GPU clusters | single-cluster cloud | multi-cluster, isolated networks |

### 1. Local (in-process)

Runs entirely within a single process. Default when using piper as a library.

```go
p, _ := piper.New(piper.Config{OutputDir: "./outputs"})
result, _ := p.Run(ctx, yamlBytes)
```

### 2. Bare-metal Worker

Server and workers run separately. Workers poll the master for tasks.

```bash
# Terminal 1: server
piper server

# Terminal 2: worker (use labels to route specific steps)
piper worker --master=http://localhost:8080 --label=gpu --concurrency=4
```

Workers register with the master on startup and send a heartbeat every 10 seconds.
Active workers can be listed via `GET /api/workers`.

To pin an entire pipeline run to one bare-metal worker, set `spec.placement.worker`.
The worker can be addressed by worker ID or hostname. `runner.label` is still supported
for compatibility, but `spec.placement` is the preferred run-level placement model.

### 3. Kubernetes Jobs (direct)

Each step runs in its own Pod. An `initContainer` copies the `piper` binary into the step container at runtime — no changes to user images required. The piper server connects directly to the Kubernetes API server.

```
initContainer (piper image)  →  cp /piper /piper-tools/piper
step container (user image)  →  /piper-tools/piper agent exec --task=<encoded-task> ...
```

```yaml
# .piper.yaml
k8s:
  agent_image: piper/piper:latest
  namespace: piper-jobs
  default_image: alpine:3.20
  ttl_after_finished: 300
```

```bash
piper server --config .piper.yaml
```

> **vs Argo Workflows**: Argo is a Kubernetes operator that manages workflows as CRDs.
> Piper is an application that uses K8s only as a compute backend — the DAG, retry logic,
> and state live in the Piper server, not in K8s. Piper runs identically in local and bare-metal
> modes without any K8s dependency.

### 4. K8s Worker

A lightweight `piper k8s-worker` process runs **inside the cluster** and connects to the piper master
over an outbound WebSocket tunnel. The master never needs inbound access to the cluster — which makes
this the right choice for isolated networks, firewall-restricted environments, or multi-cluster setups.

```
piper-server  ←──── WebSocket tunnel ────  piper k8s-worker (in-cluster)
                                                    │
                                             K8s API server
                                        (Jobs / StatefulSets / PVCs)
```

**Enable on the server side:**

```yaml
# .piper.yaml
k8s:
  worker: true          # pipeline steps routed to worker

notebook_k8s:
  worker: true          # notebook servers routed to worker

serving:
  worker: true          # model services routed to worker
```

When `worker: true` is enabled, the server still uses the same workload router for
bare-metal and K8s targets. Bare-metal workers keep their existing direct HTTP
transport; K8s workers use outbound WebSocket RPC. The user-facing placement model is
the same: choose a worker or cluster, and Piper selects a worker with the required
capability.

**Deploy the worker in the cluster:**

```bash
piper k8s-worker \
  --master  http://piper-server:8080 \
  --cluster my-cluster \
  --in-cluster \
  --pipeline-namespace  piper-jobs \
  --notebook-namespace  piper-notebooks \
  --serving-namespace   piper-serving \
  --notebook-image      jupyter/scipy-notebook:latest \
  --pipeline-worker-image piper/piper:latest \
  --storage-class       standard \
  --storage-size        20Gi \
  --s3-endpoint         minio:9000 \
  --s3-access-key       <key> \
  --s3-secret-key       <secret> \
  --s3-bucket           piper-artifacts
```

Or as a Kubernetes Deployment (recommended):

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: piper-k8s-worker
  namespace: piper-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: piper-k8s-worker
  template:
    metadata:
      labels:
        app: piper-k8s-worker
    spec:
      serviceAccountName: piper-k8s-worker   # needs Job/StatefulSet/PVC permissions
      containers:
      - name: worker
        image: piper/piper:latest
        args:
        - k8s-worker
        - --master=http://piper-server.piper-system.svc.cluster.local:8080
        - --cluster=production
        - --in-cluster
        - --pipeline-namespace=piper-jobs
        - --notebook-namespace=piper-notebooks
        - --serving-namespace=piper-serving
        - --notebook-image=jupyter/scipy-notebook:latest
        - --pipeline-worker-image=piper/piper:latest
        - --storage-class=standard
        env:
        - name: S3_ENDPOINT
          valueFrom:
            secretKeyRef:
              name: piper-s3
              key: endpoint
```

For **multi-cluster**, deploy one `piper k8s-worker` per cluster with a different `--cluster` name.
Pipeline placement is run-level: one pipeline run executes on one selected worker or cluster.

## Worker Registration API

```
POST /api/workers                    register a worker
POST /api/workers/{id}/heartbeat     keep-alive (every 10s)
GET  /api/workers                    list active workers
```

Workers handle this automatically — no manual calls needed.

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

  # Runtime: any server covered by image + command
  runtime:
    image: nvcr.io/nvidia/tritonserver:24.01-py3
    command: ["tritonserver", "--model-repository=$(PIPER_MODEL_DIR)"]
    port: 8000
    mode: local               # local | k8s

  # K8s mode only
  k8s:
    namespace: default
    replicas: 1
    image_pull_policy: IfNotPresent   # Always | IfNotPresent | Never (default: Always)
    resources:
      cpu: "2"
      memory: "8Gi"
      gpu: "1"
```

### Injected Environment Variables

| Variable | Description |
|----------|-------------|
| `PIPER_MODEL_DIR` | Path to the downloaded artifact (local mode) or S3 URI (k8s mode) |
| `PIPER_SERVICE_NAME` | ModelService name |

### Runtime Examples

```yaml
# Triton
runtime:
  image: nvcr.io/nvidia/tritonserver:24.01-py3
  command: ["tritonserver", "--model-repository=$(PIPER_MODEL_DIR)"]
  port: 8000

# vLLM
runtime:
  image: vllm/vllm-openai:latest
  command: ["python", "-m", "vllm.entrypoints.openai.api_server",
            "--model", "$(PIPER_MODEL_DIR)", "--port", "8000"]
  port: 8000

# TorchServe
runtime:
  image: pytorch/torchserve:latest
  command: ["torchserve", "--start", "--model-store", "$(PIPER_MODEL_DIR)", "--foreground"]
  port: 8080

# BentoML
runtime:
  image: my-bentoml-service:latest
  command: ["bentoml", "serve", "$(PIPER_MODEL_DIR)", "--port", "3000"]
  port: 3000
```

### Serving Worker (bare-metal local mode)

`mode: local` deploys the serving process on a **serving worker** — a separate process that runs on the bare-metal node where the model should be served.

**Requirements**
- The runtime binary (`tritonserver`, `python`, etc.) must be installed and in `PATH` on the worker node.

**Setup**

```bash
# 1. Start the piper server
piper server --addr :8080

# 2. Start a serving worker on the inference node
piper serving-worker \
  --master http://<server>:8080 \
  --addr :7700 \
  --advertise-addr http://<this-node>:7700   # omit if same machine as master
```

**Multi-node**: run `piper serving-worker` on each inference node. To pin a deployment to a specific node, set `runtime.worker` in the ModelService YAML:

```yaml
spec:
  runtime:
    mode: local
    worker: gpu-node-01   # hostname of the target serving worker
    command: [...]
    port: 8000
```

If `serving.worker: true` is set, Piper uses the unified worker router. A bare-metal
serving worker is selected by `runtime.worker`; a K8s worker is selected by cluster
placement/config and receives the request over the WebSocket tunnel.

**TLS**

```bash
piper serving-worker \
  --master https://master:8080 \
  --addr :7700 \
  --tls-cert /etc/ssl/worker.crt \
  --tls-key  /etc/ssl/worker.key \
  --advertise-addr https://<this-node>:7700
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
GET    /services                     list services
POST   /services                     deploy a ModelService (body: {"yaml": "..."})
GET    /services/{name}              service status
DELETE /services/{name}              stop and delete
POST   /services/{name}/restart      restart with latest artifact
POST   /services/predict/{name}[/…]  proxy inference request to the runtime
```

### Library Usage

```go
// Deploy a service
svc, err := p.DeployService(ctx, []byte(modelServiceYAML))

// Inference proxy is automatically mounted at /services/predict/{name}
// — just send requests to the piper server

// Stop a service
err = p.StopService(ctx, "fraud-detector")

// Restart with latest artifact
err = p.RestartService(ctx, "fraud-detector")
```

---

## Notebook Servers

Launch JupyterLab servers through the piper UI (`/notebooks`). Three deployment modes are supported.

### Mode 1: Bare-metal Worker

JupyterLab is launched as a subprocess on a dedicated **notebook worker** node.

**Requirements**: JupyterLab must be installed on each notebook worker node.

```bash
# Install JupyterLab (one-time, on each worker node)
pip install jupyterlab

# Start piper server
piper server --addr :8080

# Start notebook worker on each node
piper notebook-worker \
  --master http://<server>:8080 \
  --addr :7701 \
  --advertise-addr http://<this-node>:7701 \
  --gpus 0,1    # GPU device indices available on this node (optional)
```

**Single-node / dev** — embed everything in one process:

```bash
piper server --local
```

Bare-metal notebook workers are mirrored into the unified agent registry. If
`notebook_k8s.worker: true` is enabled and the selected notebook worker is bare-metal,
Piper uses the existing notebook worker HTTP protocol; if it is a K8s worker, Piper
uses tunnel RPC.

### Mode 2: Kubernetes (direct)

The piper server creates StatefulSets and PVCs directly via the Kubernetes API.
Use this when the piper server has direct access to the cluster's API server.

```yaml
# .piper.yaml
notebook_k8s:
  worker_image: jupyter/scipy-notebook:latest
  namespace: piper-notebooks
  storage_class: standard
  storage_size: 20Gi
  pod_defaults:
    resources:
      cpu: "2"
      memory: "8Gi"
      gpu: "1"
    node_selector:
      accelerator: nvidia-tesla-a100
    tolerations:
      - key: nvidia.com/gpu
        operator: Exists
        effect: NoSchedule
    annotations:
      iam.amazonaws.com/role: ml-notebook-role
```

### Mode 3: K8s Worker

The notebook lifecycle is managed by a `piper k8s-worker` running inside the cluster.
Use this when the piper server cannot reach the cluster API server directly (firewall, VPN, multi-cluster).

```yaml
# .piper.yaml
notebook_k8s:
  worker: true
```

Then deploy `piper k8s-worker` as described in the [K8s Worker](#4-k8s-worker) section above.

### Notebook Server YAML

```yaml
apiVersion: piper/v1
kind: NotebookServer
metadata:
  name: my-notebook
spec:
  runtime:
    port: 8888
    work_dir: ./notebooks
    gpus: "0"            # GPU device index (bare-metal mode); omit for CPU-only
    worker: gpu-node-01  # pin to a specific worker node (optional)
  storage_size: 20Gi     # PVC size (K8s modes only)
```

### TLS (bare-metal worker)

```bash
piper notebook-worker \
  --master https://master:8080 \
  --addr :7701 \
  --tls-cert /etc/ssl/worker.crt \
  --tls-key  /etc/ssl/worker.key \
  --advertise-addr https://<this-node>:7701
```

---

## Library Usage

```go
import piper "github.com/piper/piper"

p, err := piper.New(piper.Config{
    DBPath:    "./piper.db",
    OutputDir: "./outputs",
    Server:    piper.ServerConfig{Addr: ":8080"},
    Hooks: piper.Hooks{
        Auth: func(r *http.Request) error {
            // authentication logic
            return nil
        },
    },
})

// Run locally (in-process)
result, err := p.Run(ctx, yamlBytes)

// Start as HTTP server (UI + API)
err = p.Serve(ctx, piper.ServeOption{})

// Mount into an existing router (API only)
mux.Handle("/piper/", http.StripPrefix("/piper", p.Handler(nil)))

// Mount UI separately (opt-in)
import "github.com/piper/piper/pkg/ui"
mux.Handle("/piper/ui/", http.StripPrefix("/piper/ui", ui.Handler()))

// Enable Kubernetes mode
launcher, _ := k8s.New(k8s.Config{...})
p.SetBackend(launcher)

// Deploy a ModelService
svc, err := p.DeployService(ctx, []byte(modelServiceYAML))
```

## Configuration (`.piper.yaml`)

```yaml
server:
  addr: ":8080"
  tls:
    enabled: false
    cert_file: ""
    key_file: ""

run:
  output_dir: ./piper-outputs
  retries: 2
  retry_delay: 5s
  concurrency: 4

source:
  s3:
    endpoint: localhost:9000    # any S3-compatible endpoint (AWS, SeaweedFS, etc.)
    access_key: <access-key>
    secret_key: <secret-key>
    bucket: piper-artifacts

serving:
  model_dir: ./piper-models    # local artifact download path
  worker: false                 # set true to route ModelServices through the worker router

# Bare-metal notebook worker defaults
notebook_worker:
  notebooks_root: ./notebooks  # base directory for notebook work dirs
  port_range: 8888-9900        # port range for JupyterLab allocation

# Kubernetes notebook mode (direct API access)
notebook_k8s:
  worker: false                 # set true to route notebooks through the worker router
  worker_image: jupyter/scipy-notebook:latest
  namespace: piper-notebooks
  storage_class: standard
  storage_size: 20Gi
  pod_defaults:
    resources:
      cpu: "2"
      memory: "8Gi"
      gpu: "1"
    node_selector: {}
    tolerations: []
    annotations: {}

# Kubernetes pipeline mode (direct API access)
k8s:
  worker: false                      # set true to route pipeline steps through k8s-worker
  agent_image: piper/piper:latest
  agent_image_pull_policy: Always   # Always | IfNotPresent | Never (default: Always)
  namespace: piper-jobs
  in_cluster: true
  master_url: http://piper-server:8080
  default_image: alpine:3.20
  ttl_after_finished: 300
```

## CLI

```
piper server                                     start server (API + UI)
piper server --local                             start server with embedded worker/serving/notebook workers
piper server --serving-kubeconfig=~/.kube/config start server with k8s ModelService support
piper run <file.yaml>                            run a pipeline locally
piper parse <file.yaml>                          validate YAML without running
piper worker --master=<url>                      start a pipeline worker (bare-metal)
piper serving-worker --master=<url>              start a serving worker (bare-metal ModelService)
piper notebook-worker --master=<url>             start a notebook worker (bare-metal JupyterLab)
piper k8s-worker --master=<url> --cluster=<name>  start a cluster-local K8s worker (pipelines + notebooks + serving)
piper agent exec --master=<url> ...              execute a step inside a K8s Pod (called automatically)
```

> **Note**: `--serving-kubeconfig` is required when running `piper server` outside a cluster and deploying
> ModelServices in `mode: k8s`. It takes precedence over the `KUBECONFIG` environment variable.

## Docker

```bash
# Build image
make docker   # produces piper/piper:latest

# Run server
docker run -p 8080:8080 piper/piper server

# Run on Kubernetes
kubectl run piper --image=piper/piper:latest -- serve --config /etc/piper/.piper.yaml
```

The same image is used as both the server, the K8s Job init container, and the K8s worker:

```
server:         piper server --config ...
init container: cp /piper /piper-tools/piper
step container: /piper-tools/piper agent exec ...
k8s worker:      piper k8s-worker --master=... --cluster=...
```

## Building

```bash
# Requirements
go 1.21+
node 18+   # only needed to rebuild the UI

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
make test-e2e            # in-process e2e (no external infra)
make test-k8s-e2e        # real K8s e2e, including k8s-worker mode
make test-integration    # requires a running Kubernetes cluster
```

## Project Layout

```
cmd/piper/             CLI entry point and cobra commands
piper.go, config.go    library entry point, import "github.com/piper/piper"
pkg/pipeline/          YAML parsing, DAG, local runner
pkg/runner/            shared execution logic (S3 download, exec, upload, report)
pkg/dispatch/notebook/ master-side notebook dispatch drivers (worker, K8s, RPC)
pkg/dispatch/serving/  master-side serving dispatch drivers (worker, K8s, RPC)
pkg/workers/baremetal/pipeline/  bare-metal pipeline worker (poll, register, heartbeat)
pkg/workers/baremetal/notebook/  bare-metal notebook worker
pkg/workers/baremetal/serving/   bare-metal serving worker
pkg/workers/k8s/                 cluster-local K8s worker coordinator (registration, tunnel, RPC)
pkg/workers/k8s/pipeline/        K8s pipeline worker implementation
pkg/workers/k8s/notebook/        K8s notebook worker implementation
pkg/workers/k8s/serving/         K8s serving worker implementation
pkg/k8s/               Kubernetes Job launcher (direct API, ExecutionBackend implementation)
pkg/executor/          step executors (command, python, notebook)
pkg/serving/           ModelService lifecycle (deploy, stop, restart, proxy)
pkg/notebook/          NotebookServer lifecycle (create, stop, restart, delete)
internal/agent/        server-side registry, routing, and RPC target model
internal/tunnel/    WebSocket reverse tunnel hub (server-side)
internal/store/     state persistence (SQLite / PostgreSQL)
pkg/ui/             embedded React SPA
examples/           usage examples
```

## Examples

| Example | Description |
|---------|-------------|
| `examples/basics/` | Sequential, parallel, and retry pipelines |
| `examples/artifacts/` | S3 artifact passing between steps |
| `examples/git-source/` | Fetching scripts from a Git repository |
| `examples/notebook/` | Jupyter notebook execution via papermill |
| `examples/bare-metal/` | Server + worker setup with label routing (Go + YAML) |
| `examples/kubernetes/` | Per-step container images on Kubernetes |
| `examples/serving/` | ModelService deployment and inference proxy |
| `examples/library/` | In-process execution via `p.Run()` |
| `examples/embedded/` | Mounting piper into an existing HTTP server |
| `examples/mlops/` | Full MLOps demo: SeaweedFS + cron + train + auto-deploy |
