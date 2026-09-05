.PHONY: build ui docker build-linux build-linux-arm64 build-linux-native test test-ui test-notebook-conformance test-e2e test-frontend-e2e test-process-notebook-e2e test-docker-pipeline-e2e test-docker-notebook-e2e test-docker-serving-e2e test-k8s-e2e test-integration demo clean proto check-deps

ARCH ?= $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
IMAGE ?= piper/piper:latest
NOTEBOOK_IMAGE ?= jupyter/minimal-notebook:latest

# Check build prerequisites
check-deps:
	@command -v go >/dev/null 2>&1 || { echo "ERROR: go is not installed"; exit 1; }
	@bash -c 'source $$HOME/.nvm/nvm.sh 2>/dev/null; \
	  REQUIRED=$$(cat .nvmrc | tr -d "[:space:]"); \
	  CURRENT=$$(node --version 2>/dev/null | sed "s/v//"); \
	  MAJOR=$${CURRENT%%.*}; \
	  if [ "$$MAJOR" -lt "$$REQUIRED" ] 2>/dev/null; then \
	    echo "ERROR: Node $$REQUIRED required, got $$CURRENT — run: nvm use $$REQUIRED"; exit 1; \
	  fi'
	@command -v pnpm >/dev/null 2>&1 || { echo "ERROR: pnpm is not installed — run: npm i -g pnpm"; exit 1; }
	@echo "✓ dependencies OK"

# Regenerate protobuf / gRPC Go code from proto/agent.proto
proto:
	PATH="$(shell go env GOPATH)/bin:$$PATH" buf generate

# Full build (UI → Go). -tags builtinassets embeds internal/ui/dist, which
# `ui` (below) must populate first — a plain `go build ./cmd/piper` without
# this tag (e.g. `go install`) intentionally produces a binary with no UI;
# see internal/ui/ui_stub.go.
build: check-deps ui
	go build -tags builtinassets -o bin/piper ./cmd/piper

# Static build for linux/amd64 (Dockerfile uses bin/piper-amd64)
build-linux: ui
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
	go build -tags builtinassets -ldflags="-s -w" -o bin/piper-amd64 ./cmd/piper

# Static build for linux/arm64
build-linux-arm64: ui
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
	go build -tags builtinassets -ldflags="-s -w" -o bin/piper-arm64 ./cmd/piper

# Static Linux build matching the current Docker host architecture.
build-linux-native: ui
	GOOS=linux GOARCH=$(ARCH) CGO_ENABLED=0 \
	go build -tags builtinassets -ldflags="-s -w" -o bin/piper-$(ARCH) ./cmd/piper

# Build the React UI into internal/ui/dist. Not committed (see
# internal/ui/dist/.gitignore) — every build target above that ships the
# UI depends on this target instead, so the frontend is always rebuilt
# fresh rather than trusting a stale checked-in copy.
ui:
	cd frontend && pnpm run build
	rm -rf internal/ui/dist
	cp -r frontend/dist internal/ui/dist

# Runs the internal/ui tests that need a real UI build to mean anything
# (ui_embed_test.go, gated behind the builtinassets tag) — run after `ui`.
test-ui: ui
	go test -tags builtinassets ./internal/ui/...

# Build Docker image used by the server and direct-runtime workload Jobs.
docker: build-linux-native
	docker build --build-arg TARGETARCH=$(ARCH) -t $(IMAGE) .

# Run tests
test:
	go test ./...

test-notebook-conformance:
	go test ./pkg/notebook ./pkg/notebook/notebookdriver/process ./pkg/notebook/notebookdriver/docker ./pkg/notebook/dispatch/localdriver ./pkg/notebook/dispatch/localdriver/k8s

# E2E tests (fully hermetic, no external infra required)
test-e2e:
	go test -tags=e2e -v -timeout=120s ./...

test-frontend-e2e:
	cd frontend && pnpm test:e2e

test-process-notebook-e2e:
	PIPER_NOTEBOOK_PROCESS_E2E=1 \
	PIPER_NOTEBOOK_PROCESS_E2E_ENV=$(NOTEBOOK_PROCESS_ENV) \
	go test ./pkg/notebook/notebookdriver/process -run '^TestProcessRuntimeE2E_' -v -count=1 -timeout=6m

test-docker-pipeline-e2e: build-linux-native
	PIPER_DOCKER_AGENT_BINARY=$(CURDIR)/bin/piper-$(ARCH) \
	PIPER_PIPELINE_DOCKER_E2E_IMAGE=alpine:3.20 \
	go test ./pkg/pipeline/pipelinedriver/docker -run '^TestDockerRuntimeE2E_' -v -count=1 -timeout=6m

test-docker-notebook-e2e:
	PIPER_NOTEBOOK_DOCKER_E2E_IMAGE=$(NOTEBOOK_IMAGE) \
	go test ./pkg/notebook/notebookdriver/docker -run '^TestDockerRuntimeE2E_' -v -count=1 -timeout=6m

test-docker-serving-e2e:
	PIPER_SERVING_DOCKER_E2E_IMAGE=python:3.12-slim \
	go test ./pkg/serving/servingdriver/docker -run '^TestServingDockerE2E_' -v -count=1 -timeout=6m

# K8s smoke E2E (requires kubectl + a cluster with $(IMAGE) available)
test-k8s-e2e:
	PIPER_K8S_E2E_IMAGE=$(IMAGE) \
	PIPER_K8S_E2E_NOTEBOOK_IMAGE=$(NOTEBOOK_IMAGE) \
	go test -tags=k8s_e2e -v -timeout=60m .

# Integration tests (requires a K8s cluster)
test-integration:
	go test ./pkg/k8s/... -tags=integration -v

# Full MLOps demo: SeaweedFS (S3) + piper server + worker + schedule + auto-deploy
# Prerequisites: Docker, Python 3.9+, pip install scikit-learn
demo: build
	bash examples/mlops/setup.sh

# Tear down demo storage
demo-down:
	docker compose -f examples/mlops/docker-compose.yml down -v

clean:
	rm -rf bin/ internal/ui/dist frontend/dist
