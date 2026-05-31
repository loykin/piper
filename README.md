# piper

A lightweight pipeline orchestrator inspired by GitHub Actions and GitLab CI.
Runs as a standalone server or embedded as a Go library.

## Features

- **DAG-based execution** — declare step dependencies with `depends_on`, automatic parallelization
- **Three execution modes** — local (in-process) / bare-metal worker / Kubernetes Jobs
- **Single binary** — UI included, works out of the box with `go install`
- **Embeddable library** — mount into your existing Go app and HTTP router
- **S3 artifact passing** — share files between steps via any S3-compatible store (AWS S3, SeaweedFS, etc.)
- **Real-time logs** — SSE streaming, visible in the web UI

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

| | Local | Bare-metal Worker | Kubernetes |
|---|---|---|---|
| **Runs on** | single process | separate worker processes | K8s Jobs (one Pod per step) |
| **Task delivery** | direct call | HTTP polling `/api/tasks/next` | K8s Job created per step |
| **Artifact storage** | local filesystem | shared filesystem or S3 | S3 required |
| **Cancellation** | context cancel | SSE event | `kubectl delete job` |
| **Best for** | development, libraries | on-prem GPU clusters | cloud-native, auto-scaling |

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

### 3. Kubernetes Jobs

Each step runs in its own Pod. An `initContainer` copies the `piper` binary into the step container at runtime — no changes to user images required.

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
  master_url: http://piper-server.default.svc.cluster.local:8080
  ttl_after_finished: 300
```

```bash
piper server --config .piper.yaml
```

> **vs Argo Workflows**: Argo is a Kubernetes operator that manages workflows as CRDs.
> Piper is an application that uses K8s only as a compute backend — the DAG, retry logic,
> and state live in the Piper server, not in K8s. Piper runs identically in local and bare-metal
> modes without any K8s dependency.

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

`mode: local` deploys the serving process on a **serving worker** agent — a separate process that runs on the bare-metal node where the model should be served.

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

Launch Jupyter Lab servers on bare-metal nodes through the piper UI (`/notebooks`).

### Requirements

JupyterLab must be installed on each notebook worker node. The worker launches `jupyter-lab` as a subprocess, so it must be accessible from the system `PATH` (or configured via `notebook_worker.jupyter_bin`).

**macOS (brew) — recommended**

```bash
brew install jupyterlab
# binary lands at /usr/local/opt/jupyterlab/bin/jupyter-lab (Intel)
#                  /opt/homebrew/opt/jupyterlab/bin/jupyter-lab (Apple Silicon)
# brew also creates a symlink: /usr/local/bin/jupyter-lab
```

Config (`.piper.yaml`):
```yaml
notebook_worker:
  jupyter_bin: /usr/local/opt/jupyterlab/bin/jupyter-lab   # Intel Mac
  # jupyter_bin: /opt/homebrew/opt/jupyterlab/bin/jupyter-lab  # Apple Silicon
```

**Linux (pip, system-wide) — recommended for servers**

```bash
pip install jupyterlab          # installs to /usr/local/bin/jupyter or ~/.local/bin/jupyter
# or with sudo for all users:
sudo pip install jupyterlab
```

No config needed if `/usr/local/bin` or `~/.local/bin` is in `PATH`.

**Linux/macOS (conda)**

```bash
conda install -c conda-forge jupyterlab
# binary at: $CONDA_PREFIX/bin/jupyter-lab
```

Config:
```yaml
notebook_worker:
  jupyter_bin: /opt/conda/bin/jupyter-lab   # adjust to your conda env path
```

**Virtual environment (venv)**

> **Not recommended for the worker process.** The worker is a long-running daemon — venvs require activation which doesn't apply to subprocesses launched by Go. Use a system-wide or conda install instead, and point `jupyter_bin` at the full path inside the venv only if you know what you're doing:

```yaml
notebook_worker:
  jupyter_bin: /path/to/venv/bin/jupyter-lab
```

**Verify your install**

```bash
which jupyter-lab || which jupyter
jupyter-lab --version
```

### Setup

```bash
# 1. Start the piper server
piper server --addr :8080

# 2. Start a notebook worker on each node
piper notebook-worker \
  --master http://<server>:8080 \
  --addr :7701 \
  --advertise-addr http://<this-node>:7701   # omit if same machine as master
```

**Single-node / dev** — embed everything in one process:

```bash
piper server --local
```

### Custom jupyter binary path

If jupyter is not in `PATH` (e.g. brew on macOS), configure it once in `.piper.yaml`:

```yaml
notebook_worker:
  jupyter_bin: /usr/local/opt/jupyterlab/bin/jupyter-lab
```

Or pass directly to the standalone worker:

```bash
piper notebook-worker \
  --master http://localhost:8080 \
  --jupyter-bin /usr/local/opt/jupyterlab/bin/jupyter-lab
```

> **Finding your jupyter binary**: run `which jupyter-lab || which jupyter` in a terminal.

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
    gpus: "0"            # GPU device index; omit for CPU-only
    worker: gpu-node-01  # pin to a specific worker node (optional)
```

### TLS

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

notebook_worker:
  jupyter_bin: ""              # path to jupyter binary (default: "jupyter" in PATH)
                               # e.g. /usr/local/opt/jupyterlab/bin/jupyter-lab

k8s:
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
piper worker --master=<url>                      start a pipeline worker
piper serving-worker --master=<url>              start a serving worker (bare-metal ModelService)
piper notebook-worker --master=<url>             start a notebook worker (Jupyter Lab)
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

The same image is used for both the server and K8s Job injection:

```
initContainer:  cp /piper /piper-tools/piper
step container: /piper-tools/piper agent exec ...
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
make test-integration    # requires a running Kubernetes cluster
```

## Project Layout

```
cmd/piper/          CLI entry point and cobra commands
piper.go, config.go library entry point — import "github.com/piper/piper"
pkg/pipeline/       YAML parsing, DAG, local runner
pkg/runner/         shared execution logic (S3 download → exec → upload → report)
pkg/worker/         bare-metal worker (poll, register, heartbeat)
pkg/k8s/            Kubernetes Job launcher (ExecutionBackend implementation)
pkg/executor/       step executors (command, python, notebook)
pkg/serving/        ModelService lifecycle (deploy, stop, restart, proxy)
internal/store/     state persistence (SQLite)
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
