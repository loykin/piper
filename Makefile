.PHONY: build ui docker test test-e2e test-integration demo clean

ARCH ?= $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
IMAGE ?= piper/piper:latest

# Full build (UI → Go)
build: ui
	go build -o bin/piper ./cmd/piper

# Static build for linux/arm64 or amd64 (for Docker)
build-linux:
	GOOS=linux GOARCH=$(ARCH) CGO_ENABLED=0 \
	go build -ldflags="-s -w" -o bin/piper ./cmd/piper

# Build the React UI and update pkg/ui/dist (commit after building)
ui:
	cd frontend && npm run build
	rm -rf pkg/ui/dist
	cp -r frontend/dist pkg/ui/dist
	@echo "UI built. Commit pkg/ui/dist/ to include in go install."

# Build Docker image (serves as both server and K8s agent)
docker: build-linux
	docker build -t $(IMAGE) .

# Run tests
test:
	go test ./...

# E2E tests (fully hermetic, no external infra required)
test-e2e:
	go test -tags=e2e -v -timeout=120s ./...

# Integration tests (requires a K8s cluster)
test-integration:
	go test ./pkg/k8s/... -tags=integration -v

# Full MLOps demo: SeaweedFS (S3) + piper server + worker + schedule + auto-deploy
# Prerequisites: Docker, Python 3.9+, pip install scikit-learn
demo: build
	bash examples/full-mlops/setup.sh

# Tear down demo storage
demo-down:
	docker compose -f examples/full-mlops/docker-compose.yml down -v

clean:
	rm -rf bin/ pkg/ui/dist frontend/dist
