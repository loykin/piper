#!/bin/bash
# Full MLOps demo setup
#
# What this does:
#   1. Starts SeaweedFS (S3 storage) via docker-compose
#   2. Builds the piper binary
#   3. Registers the iris-predictor ModelService
#   4. Creates an hourly cron schedule for iris-retraining
#   5. Triggers a one-off run so you see results immediately
#
# Prerequisites:
#   - Docker + Docker Compose
#   - Go 1.21+
#   - Python 3.9+  with scikit-learn, flask installed
#       pip install scikit-learn flask
#
# Usage:
#   cd <repo-root>
#   bash examples/full-mlops/setup.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT"

SERVER="${PIPER_SERVER:-http://localhost:8080}"
CRON="${PIPER_CRON:-0 * * * *}"          # default: every hour on the hour
DEMO_CRON="${DEMO_CRON:-}"               # set to "*/5 * * * *" to run every 5 min for demo

# ── 1. Start storage ──────────────────────────────────────────────────────────
echo "==> Starting SeaweedFS (S3) via docker-compose…"
docker compose -f examples/full-mlops/docker-compose.yml up -d --wait
echo "    SeaweedFS S3 ready at http://localhost:8333"

# ── 2. Build piper ────────────────────────────────────────────────────────────
echo "==> Building piper…"
go build -o bin/piper ./cmd/piper

# ── 3. Start piper server (background) ───────────────────────────────────────
echo "==> Starting piper server on :8080…"
bin/piper --config examples/full-mlops/piper.yaml server &
PIPER_PID=$!
echo "    piper server PID=$PIPER_PID"

# Wait for the server to be ready
for i in $(seq 1 20); do
    if curl -sf "$SERVER/runs" > /dev/null 2>&1; then
        break
    fi
    echo "    waiting for server… ($i/20)"
    sleep 1
done
curl -sf "$SERVER/runs" > /dev/null || { echo "ERROR: piper server did not start"; exit 1; }
echo "    server ready"

# ── 4. Start piper worker (background) ───────────────────────────────────────
echo "==> Starting piper worker…"
bin/piper --config examples/full-mlops/piper.yaml worker --master "$SERVER" &
WORKER_PID=$!
echo "    worker PID=$WORKER_PID"

# ── 5. Register ModelService ──────────────────────────────────────────────────
echo "==> Registering iris-predictor service…"
SERVICE_YAML=$(python3 -c "
import sys, json
with open('examples/full-mlops/iris-service.yaml') as f:
    print(json.dumps(f.read()))
")
curl -sf "$SERVER/services" \
    -H 'Content-Type: application/json' \
    -d "{\"yaml\": $SERVICE_YAML}" \
| python3 -m json.tool
echo ""

# ── 6. Create cron schedule ───────────────────────────────────────────────────
ACTIVE_CRON="${DEMO_CRON:-$CRON}"
echo "==> Creating cron schedule: '$ACTIVE_CRON'"
PIPELINE_YAML=$(python3 -c "
import sys, json
with open('examples/full-mlops/iris-pipeline.yaml') as f:
    print(json.dumps(f.read()))
")
SCHEDULE=$(curl -sf "$SERVER/schedules" \
    -H 'Content-Type: application/json' \
    -d "{
      \"name\": \"iris-retraining-hourly\",
      \"type\": \"cron\",
      \"cron\": \"$ACTIVE_CRON\",
      \"yaml\": $PIPELINE_YAML
    }")
echo "$SCHEDULE" | python3 -m json.tool
SCHEDULE_ID=$(echo "$SCHEDULE" | python3 -c "import sys,json; print(json.load(sys.stdin).get('id',''))")
echo ""

# ── 7. Trigger one immediate run ──────────────────────────────────────────────
echo "==> Triggering immediate run (so you don't have to wait for the cron)…"
RUN=$(curl -sf "$SERVER/runs" \
    -H 'Content-Type: application/json' \
    -d "{\"yaml\": $PIPELINE_YAML}")
echo "$RUN" | python3 -m json.tool
RUN_ID=$(echo "$RUN" | python3 -c "import sys,json; print(json.load(sys.stdin).get('run_id',''))")
echo ""

# ── 8. Wait for the run to complete ──────────────────────────────────────────
echo "==> Waiting for run $RUN_ID to complete…"
for i in $(seq 1 60); do
    STATUS=$(curl -sf "$SERVER/runs/$RUN_ID" | python3 -c "
import sys,json; d=json.load(sys.stdin); print(d['run']['status'])
" 2>/dev/null || echo "unknown")
    echo "    [$i/60] status=$STATUS"
    case "$STATUS" in
        success)
            echo "    Run SUCCEEDED"
            break
            ;;
        failed|canceled)
            echo "    Run $STATUS — check logs at $SERVER/runs/$RUN_ID"
            kill "$PIPER_PID" "$WORKER_PID" 2>/dev/null || true
            exit 1
            ;;
    esac
    sleep 3
done

# ── 9. Show metrics ────────────────────────────────────────────────────────────
echo ""
echo "==> Run metrics:"
curl -sf "$SERVER/runs/$RUN_ID/metrics" | python3 -m json.tool

# ── 10. Show service status ────────────────────────────────────────────────────
echo ""
echo "==> iris-predictor service status:"
curl -sf "$SERVER/services/iris-predictor" | python3 -m json.tool

# ── 11. Smoke-test the prediction endpoint ────────────────────────────────────
echo ""
echo "==> Testing prediction endpoint (http://localhost:9080/predict)…"
sleep 2   # give the service process a moment to bind the port
curl -sf http://localhost:9080/predict \
    -H 'Content-Type: application/json' \
    -d '{"features": [5.1, 3.5, 1.4, 0.2]}' \
| python3 -m json.tool

echo ""
echo "╔══════════════════════════════════════════════════════╗"
echo "║  Full MLOps loop running!                           ║"
echo "║                                                     ║"
echo "║  UI:         http://localhost:8080                  ║"
echo "║  Predict:    http://localhost:9080/predict          ║"
echo "║  Schedule:   $ACTIVE_CRON                           ║"
echo "║                                                     ║"
echo "║  Ctrl+C to stop piper; docker compose down to      ║"
echo "║  stop SeaweedFS.                                    ║"
echo "╚══════════════════════════════════════════════════════╝"

# Keep running so piper server + worker stay alive
wait "$PIPER_PID"
