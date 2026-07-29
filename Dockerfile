# Single piper image build
# - Server:    docker run ghcr.io/loykin/piper:latest server
# - K8s agent: initContainer copies the /piper binary for use
#
# Build (local):
#   make docker
#
# Multi-arch build (CI):
#   docker buildx build --platform linux/amd64,linux/arm64 -t ...
#   Pre-build binaries: bin/piper-amd64, bin/piper-arm64

# TARGETARCH is set automatically by buildx (amd64 or arm64).
ARG TARGETARCH=amd64

FROM alpine:3.20
ARG TARGETARCH
RUN apk add --no-cache ca-certificates tzdata
# Copy the architecture-specific binary.
# Local builds (make docker) use the Linux binary matching the Docker host.
COPY bin/piper-${TARGETARCH} /piper
ENTRYPOINT ["/piper"]
