# piper

Piper is a single-binary, worker-based runner for ML jobs, notebooks, and model
services.

It is for teams that need more than cron, shell scripts, or CI jobs, but do not
want to operate a full ML platform such as Kubeflow. Piper runs as a standalone
server or embedded Go library, and can dispatch work to local processes,
bare-metal GPU workers, or cluster-local K8s workers.

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
  # Optional: default driver settings applied to all steps.
  # Use placement to pin the whole run to one worker.
  defaults:
    driver:
      placement:
        worker: ""                # worker ID/hostname (bare-metal or k8s-worker ID)
        label: ""                 # route to any worker registered with this label
        runtime: ""               # baremetal | docker | k8s
      docker:
        image: alpine:3.20        # default image for Docker runtime
      k8s:
        image: alpine:3.20        # default image for K8s runtime
        namespace: ""             # K8s namespace for worker-created Jobs

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
      timeout: 600
    driver:
      placement:
        label: gpu                # route to workers with this label
        runtime: k8s
      resources:
        cpu: "4"
        memory: "16Gi"
        gpu: "1"
      k8s:
        image: python:3.12-slim   # image for this step (overrides defaults.driver.k8s.image)
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

| | Local | Bare-metal Worker | K8s Worker |
|---|---|---|---|
| **Step runs as** | goroutine in server process | subprocess or Docker container | K8s Job (one Pod per step) |
| **Task delivery** | in-process (no queue, no gRPC) | gRPC push (worker → server outbound) | gRPC push (worker → server outbound) |
| **Worker location** | — | any machine | inside the K8s cluster |
| **Server needs kubeconfig** | — | no | no — worker owns cluster access |
| **Artifact storage** | local or S3 | local or S3 | S3 required (no shared filesystem) |
| **Cancellation** | context cancel | SIGTERM / docker stop | K8s Job deletion |
| **Multi-cluster** | — | — | yes (one k8s-worker per cluster) |
| **Best for** | development, libraries | on-prem GPU servers | Kubernetes environments |

### 1. Local (in-process)

Runs entirely within a single process. Default when using piper as a library.

```go
p, _ := piper.New(piper.Config{OutputDir: "./outputs"})
result, _ := p.Run(ctx, yamlBytes)
```

### 2. Bare-metal Worker

Server and workers run separately. Workers connect to the master via gRPC and receive tasks by push.

```bash
# Terminal 1: server (start gRPC agent server on :9090)
piper server --agent-addr :9090

# Terminal 2: worker (connect to gRPC agent server)
piper worker --agent-addr localhost:9090 --master http://localhost:8080 --label gpu --concurrency 4
```

- `--agent-addr`: gRPC address of the master's agent server (required)
- `--master`: piper HTTP URL, used by each step subprocess for artifact upload/reporting
- `--label`: label for routing (e.g. `gpu`, `cpu`, `large-mem`)

Workers register with the master on connect. Active workers can be listed via `GET /api/agents`.

To pin an entire pipeline run to one bare-metal worker, set `spec.defaults.driver.placement.worker`.
To route all steps to workers with a specific label, set `spec.defaults.driver.placement.label`.
Individual steps can override placement via `step.driver.placement`.

### 3. K8s Worker

A `piper k8s-worker` runs **inside the cluster** and connects outbound to the piper master's gRPC agent server. The master never needs kubeconfig access or inbound cluster access — making this the right choice for isolated networks, firewall-restricted environments, or multi-cluster setups.

```
piper-server (:9090 gRPC) ←──── outbound gRPC ────  piper k8s-worker (in-cluster)
                                                              │
                                                       K8s API server
                                                  (Jobs / StatefulSets / PVCs)
```

Each pipeline step runs as a K8s Job. An `initContainer` copies the `piper` binary into the step container — no changes to user images required:

```
initContainer (piper image)  →  cp /piper /piper-tools/piper
step container (user image)  →  /piper-tools/piper agent exec --task=<encoded-task> ...
```

> **vs Argo Workflows**: Argo manages workflows as K8s CRDs. Piper uses K8s only as a compute backend — DAG, retry, and state live in the Piper server. Piper runs identically in local and bare-metal modes with no K8s dependency.

**Enable on the server side:**

```yaml
# .piper.yaml
pipeline:

k8s:
  worker: true          # pipeline steps routed to k8s-worker

notebook_k8s:
  worker: true          # notebook servers routed to k8s-worker

serving:
  worker: true          # model services routed to k8s-worker
```

**Deploy the worker in the cluster:**

```bash
piper k8s-worker \
  --master  http://piper-server:8080 \
  --cluster my-cluster \
  --in-cluster \
  --enable  pipeline,notebook,serving \    # default: all three; omit to enable all
  --pipeline-namespace  piper-jobs \
  --notebook-namespace  piper-notebooks \
  --serving-namespace   piper-serving \
  --notebook-image      jupyter/scipy-notebook:latest \
  --pipeline-worker-image piper/piper:latest \
  --storage-class       standard \
  --storage-size        20Gi \
  --storage-url         "s3://piper-artifacts?endpoint=minio:9000&access_key=<key>&secret_key=<secret>"
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

## Agent API

Workers register automatically over gRPC when they connect to the master's agent server.

```
GET  /api/agents                     list connected agents (workers)
```

Pipeline workers connect to the master through the gRPC agent API. HTTP worker polling endpoints are not supported.

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
    resources:
      cpu: "2"
      memory: "8Gi"
      gpu: "1"
    k8s:                        # K8s runtime settings
      image: nvcr.io/nvidia/tritonserver:24.01-py3
      namespace: default
      replicas: 1
      image_pull_policy: IfNotPresent
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
# 1. Start the piper server with gRPC agent server
piper server --addr :8080 --agent-addr :9090

# 2. Start a serving worker on the inference node
piper serving-worker --agent-addr <server>:9090
```

**Multi-node**: run `piper serving-worker` on each inference node. To pin a deployment to a specific node, set `driver.placement.worker` in the ModelService YAML:

```yaml
spec:
  run:
    command: [...]
    port: 8000
  driver:
    placement:
      worker: gpu-node-01     # serving worker ID (default: stable serving-<hostname>)
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
GET    /serving                      list services
POST   /serving                      deploy a ModelService (body: {"yaml": "..."})
GET    /serving/{name}               service status
DELETE /serving/{name}               stop and delete
POST   /serving/{name}/restart       restart with latest artifact
POST   /serving/predict/*path        proxy inference request to the runtime
```

### Library Usage

```go
// Deploy a service
svc, err := p.DeployService(ctx, []byte(modelServiceYAML))

// Inference proxy is automatically mounted at /serving/predict/*path
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

# Start piper server with gRPC agent server
piper server --addr :8080 --agent-addr :9090

# Start notebook worker on each node
piper notebook-worker \
  --agent-addr <server>:9090 \
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
kind: Notebook
metadata:
  name: my-notebook
spec:
  volume:
    size: 20Gi              # PVC size (K8s mode only)

  options:
    env:
      - name: JUPYTER_TOKEN
        value: ""

  driver:
    placement:
      runtime: k8s          # baremetal | docker | k8s
      worker: gpu-node-01   # pin to a specific worker (optional)
    resources:
      cpu: "2"
      memory: "8Gi"
      gpu: "1"
    k8s:                    # K8s-specific (runtime=k8s)
      image: jupyter/scipy-notebook:latest
      pod_template:
        spec:
          nodeSelector:
            accelerator: nvidia
    process:                # baremetal-specific (runtime=baremetal)
      gpus: "0"             # CUDA_VISIBLE_DEVICES
    docker:                 # Docker-specific (runtime=docker)
      cpus: "4"
      mem_limit: "8g"
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

// Start with gRPC agent server for bare-metal/K8s workers
err = p.Serve(ctx, piper.ServeOption{AgentAddr: ":9090"})

// Mount into an existing router (API only)
mux.Handle("/piper/", http.StripPrefix("/piper", p.Handler(nil)))

// Mount UI separately (opt-in)
import "github.com/piper/piper/pkg/ui"
mux.Handle("/piper/ui/", http.StripPrefix("/piper/ui", ui.Handler()))

// Deploy a ModelService
svc, err := p.DeployService(ctx, []byte(modelServiceYAML))
```

## Configuration (`.piper.yaml`)

```yaml
server:
  addr: ":8080"
  agent_addr: ":9090"   # gRPC agent server for workers (required for worker modes)
  tls:
    enabled: false
    cert_file: ""
    key_file: ""

pipeline:

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
  mode: process                 # process | docker
  notebooks_root: ./notebooks  # base directory for notebook work dirs
  port_range: 8888-9900        # port range for JupyterLab allocation
  docker:
    image: jupyter/scipy-notebook:latest
    network: bridge
    cpus: "2"
    memory: 4g
    shm_size: 1g
    read_only_root: false
    tmpfs: []
    volumes: []
    extra_args: []

# Kubernetes notebook mode (worker dispatch)
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

# Kubernetes pipeline mode (worker dispatch)
k8s:
  worker: false                 # set true to route pipeline steps through k8s-worker
```

## CLI

```
piper server                                     start server (API + UI)
piper server --local                             start server with embedded worker/serving/notebook workers
piper run <file.yaml>                            run a pipeline locally
piper parse <file.yaml>                          validate YAML without running
piper worker --agent-addr=<grpc-addr> --master=<url>   start a pipeline worker (bare-metal)
piper serving-worker --agent-addr=<grpc-addr>          start a serving worker (bare-metal ModelService)
piper notebook-worker --agent-addr=<grpc-addr>         start a notebook worker (bare-metal JupyterLab)
piper k8s-worker --master=<url> --cluster=<name> start a cluster-local K8s worker (pipelines + notebooks + serving)
piper k8s-worker --master=<url> --cluster=<name> --enable pipeline  start K8s worker for pipeline only
piper agent exec --master=<url> ...              execute a step inside a K8s Pod (called automatically)
```

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
make test-notebook-conformance
make test-e2e            # in-process e2e (no external infra)
make test-docker-notebook-e2e
make test-k8s-e2e        # real K8s e2e, including k8s-worker mode
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
pkg/dispatch/notebook/          master-side notebook dispatch
pkg/dispatch/serving/           master-side serving dispatch
pkg/workers/k8s/                cluster-local K8s unified worker (pipeline + notebook + serving)
pkg/workers/k8s/pipeline/       K8s pipeline dispatch (embedded in k8s unified worker)
pkg/k8s/                        Kubernetes Job launcher primitives
pkg/executor/                   step executors (command, python, notebook)

internal/proto/                 Task, TaskResult wire types
internal/event/                 in-process pub/sub event bus
internal/logstore/              log and metric storage
internal/agent/                 server-side worker registry, routing, RPC model
internal/grpcagent/             gRPC bidirectional streaming transport
internal/queue/                 DAG task queue (retry, lease, idempotency)
internal/store/                 state persistence (SQLite / PostgreSQL)
internal/tunnel/                WebSocket reverse tunnel hub

pkg/run/                        Run and Step records (public API)
pkg/schedule/                   cron/once scheduled pipeline execution
pkg/ui/                         embedded React SPA
examples/                       usage examples
```

## Examples

| Example | Description |
|---------|-------------|
| `examples/basics/` | Sequential, parallel, and retry pipelines |
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
