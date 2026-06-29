.PHONY: build ui docker test test-notebook-conformance test-e2e test-frontend-e2e test-process-notebook-e2e test-docker-notebook-e2e test-k8s-e2e test-integration demo clean proto check-deps

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

# Full build (UI → Go)
build: check-deps ui
	go build -o bin/piper ./cmd/piper

# Static build for linux/amd64 (Dockerfile uses bin/piper-amd64)
build-linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
	go build -ldflags="-s -w" -o bin/piper-amd64 ./cmd/piper

# Static build for linux/arm64
build-linux-arm64:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
	go build -ldflags="-s -w" -o bin/piper-arm64 ./cmd/piper

# Build the React UI and update pkg/ui/dist (commit after building)
ui:
	bash -c 'source $$HOME/.nvm/nvm.sh && nvm use --silent && cd frontend && pnpm run build'
	rm -rf pkg/ui/dist
	cp -r frontend/dist pkg/ui/dist
	@echo "UI built. Commit pkg/ui/dist/ to include in go install."

# Build Docker image (serves as both server and K8s agent)
docker: build-linux
	docker build --build-arg TARGETARCH=amd64 -t $(IMAGE) .

# Run tests
test:
	go test ./...

test-notebook-conformance:
	go test ./pkg/notebook ./pkg/workers/baremetal/notebook ./pkg/workers/k8s/notebook ./pkg/dispatch/notebook ./internal/grpcagent

# E2E tests (fully hermetic, no external infra required)
test-e2e:
	go test -tags=e2e -v -timeout=120s ./...

test-frontend-e2e:
	cd frontend && pnpm test:e2e

test-process-notebook-e2e:
	PIPER_NOTEBOOK_PROCESS_E2E=1 \
	PIPER_NOTEBOOK_PROCESS_E2E_ENV=$(NOTEBOOK_PROCESS_ENV) \
	go test ./pkg/workers/baremetal/notebook -run '^TestProcessRuntimeE2E_' -v -count=1 -timeout=6m

test-docker-notebook-e2e:
	PIPER_NOTEBOOK_DOCKER_E2E_IMAGE=$(NOTEBOOK_IMAGE) \
	go test ./pkg/workers/baremetal/notebook -run '^TestDockerRuntimeE2E_' -v -count=1 -timeout=6m

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
	rm -rf bin/ pkg/ui/dist frontend/dist
