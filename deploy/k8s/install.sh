#!/usr/bin/env sh
set -eu

command -v kubectl >/dev/null 2>&1 || {
  echo "kubectl is required" >&2
  exit 1
}
command -v openssl >/dev/null 2>&1 || {
  echo "openssl is required" >&2
  exit 1
}

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

kubectl apply -f "$SCRIPT_DIR/namespace.yaml"

if ! kubectl -n piper get secret piper-server-secrets >/dev/null 2>&1; then
  AUTH_SIGNING_KEY=$(openssl rand -base64 32)
  SECRET_ENCRYPTION_KEY=$(openssl rand -base64 32)
  WORKER_TOKEN=$(openssl rand -base64 32)
  kubectl -n piper create secret generic piper-server-secrets \
    --from-literal="auth_signing_key=$AUTH_SIGNING_KEY" \
    --from-literal="secret_encryption_key=$SECRET_ENCRYPTION_KEY" \
    --from-literal="worker_token=$WORKER_TOKEN"
  echo "created piper-server-secrets"
else
  echo "reusing existing piper-server-secrets"
  if [ -z "$(kubectl -n piper get secret piper-server-secrets -o jsonpath='{.data.worker_token}')" ]; then
    WORKER_TOKEN=$(openssl rand -base64 32)
    kubectl -n piper patch secret piper-server-secrets \
      --type merge \
      -p "{\"stringData\":{\"worker_token\":\"$WORKER_TOKEN\"}}"
    echo "added missing worker_token"
  fi
fi

kubectl apply -k "$SCRIPT_DIR"
kubectl -n piper rollout status deployment/piper-server
kubectl -n piper rollout status deployment/piper-k8s-worker

echo "Piper is running. Open a local tunnel with:"
echo "  kubectl -n piper port-forward service/piper-server 8080:8080"
