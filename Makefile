.PHONY: build ui docker test clean

ARCH ?= $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
IMAGE ?= piper/piper:latest

# 전체 빌드 (UI → Go)
build: ui
	go build -o bin/piper ./cmd/piper

# linux/arm64 or amd64 정적 빌드 (Docker용)
build-linux:
	GOOS=linux GOARCH=$(ARCH) CGO_ENABLED=0 \
	go build -ldflags="-s -w" -o bin/piper ./cmd/piper

# React UI 빌드 후 pkg/ui/dist 갱신 (빌드 후 git에 커밋)
ui:
	cd frontend && npm run build
	rm -rf pkg/ui/dist
	cp -r frontend/dist pkg/ui/dist
	@echo "UI built. Commit pkg/ui/dist/ to include in go install."

# Docker 이미지 빌드 (서버 + K8s agent 겸용)
docker: build-linux
	docker build -t $(IMAGE) .

# 테스트
test:
	go test ./...

# integration test (K8s 클러스터 필요)
test-integration:
	go test ./pkg/k8s/... -tags=integration -v

clean:
	rm -rf bin/ pkg/ui/dist frontend/dist
