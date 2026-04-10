.PHONY: build ui agent test clean

ARCH ?= $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')

# 전체 빌드 (UI → Go)
build: ui
	go build -o bin/piper ./cmd/piper

# React UI 빌드 후 pkg/ui/dist 갱신 (빌드 후 git에 커밋)
ui:
	cd frontend && npm run build
	rm -rf pkg/ui/dist
	cp -r frontend/dist pkg/ui/dist
	@echo "UI built. Commit pkg/ui/dist/ to include in go install."

# piper-agent (K8s용, linux/arm64 또는 linux/amd64)
agent:
	GOOS=linux GOARCH=$(ARCH) CGO_ENABLED=0 go build -o bin/piper-agent ./cmd/agent
	docker build -t piper/agent:latest -f Dockerfile.agent .

# 테스트
test:
	go test ./...

# integration test (K8s 클러스터 필요)
test-integration:
	go test ./pkg/k8s/... -tags=integration -v

clean:
	rm -rf bin/ pkg/ui/dist frontend/dist
