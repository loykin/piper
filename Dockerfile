# piper 단일 이미지 빌드
# - 서버: docker run piper/piper piper serve
# - K8s agent: initContainer가 /piper 바이너리를 복사해서 사용
#
# 빌드:
#   make docker

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY bin/piper /piper
ENTRYPOINT ["/piper"]
