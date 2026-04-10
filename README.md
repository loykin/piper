# piper

A lightweight pipeline orchestrator inspired by GitHub Actions and GitLab CI.
Runs as a standalone server or embedded as a Go library.

## Features

- **DAG-based execution** — declare step dependencies with `depends_on`, automatic parallelization
- **Three execution modes** — local (in-process) / bare-metal worker / Kubernetes Jobs
- **Single binary** — UI included, works out of the box with `go install`
- **Embeddable library** — mount into your existing Go app and HTTP router
- **S3 artifact passing** — share files between steps via MinIO or AWS S3
- **Real-time logs** — SSE streaming, visible in the web UI

## Quick Start

```bash
go install github.com/piper/piper/cmd/piper@latest

# Start server
piper serve

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

## Execution Modes

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
piper serve

# Terminal 2: worker (use labels to route specific steps)
piper worker --master=http://localhost:8080 --label=gpu --concurrency=4
```

Workers register with the master on startup and send a heartbeat every 10 seconds.
Active workers can be listed via `GET /api/workers`.

### 3. Kubernetes Jobs

Each step runs in its own Pod. An initContainer injects the `piper` binary into the step container.

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
piper serve --config .piper.yaml
```

No changes to user container images are required.

## Worker Registration API

```
POST /api/workers                    register a worker
POST /api/workers/{id}/heartbeat     keep-alive (every 10s)
GET  /api/workers                    list active workers
```

Workers handle this automatically — no manual calls needed.

## Library Usage

```go
import "github.com/piper/piper/pkg/piper"

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
p.SetDispatcher(launcher)
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
    endpoint: localhost:9000
    access_key: minioadmin
    secret_key: minioadmin
    bucket: piper-artifacts

k8s:
  agent_image: piper/piper:latest
  namespace: piper-jobs
  in_cluster: true
  master_url: http://piper-server:8080
  default_image: alpine:3.20
  ttl_after_finished: 300
```

## CLI

```
piper serve                          start server (API + UI)
piper run <file.yaml>                run a pipeline locally
piper parse <file.yaml>              validate YAML without running
piper worker --master=<url>          start a worker
piper agent exec --master=<url> ...  execute a step inside a K8s Pod (called automatically)
```

## Docker

```bash
# Build image
make docker   # produces piper/piper:latest

# Run server
docker run -p 8080:8080 piper/piper serve

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
make test
make test-integration   # requires a running Kubernetes cluster
```

## Project Layout

```
cmd/piper/          CLI entry point
pkg/piper/          library entry point (Piper, Config, Serve)
pkg/pipeline/       YAML parsing, DAG, local runner
pkg/runner/         shared execution logic (S3 download → exec → upload → report)
pkg/worker/         bare-metal worker (poll, register, heartbeat)
pkg/k8s/            Kubernetes Job launcher (Dispatcher implementation)
pkg/executor/       step executors (command, python, notebook)
pkg/store/          state persistence (SQLite / PostgreSQL)
pkg/cmd/            cobra commands (reusable in embedded mode)
pkg/ui/             embedded React SPA
examples/           usage examples
```

## Examples

| Example | Description |
|---------|-------------|
| `examples/library/` | In-process execution via `p.Run()` |
| `examples/server/` | Standalone server mode |
| `examples/worker/` | Bare-metal worker with label routing |
| `examples/embedded/` | Mounting piper into an existing HTTP server |
| `examples/simple.yaml` | Basic sequential pipeline |
| `examples/parallel.yaml` | Fan-out / fan-in with parallel steps |
| `examples/artifacts.yaml` | S3 artifact passing between steps |
| `examples/labels.yaml` | Routing steps to specific workers |
| `examples/k8s.yaml` | Per-step container images on Kubernetes |
