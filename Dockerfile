# Single piper image build
# - Server:    docker run piper/piper piper serve
# - K8s agent: initContainer copies the /piper binary for use
#
# Build:
#   make docker

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY bin/piper /piper
ENTRYPOINT ["/piper"]
