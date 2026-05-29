"""
Iris prediction server.

Piper downloads the model artifact from S3 to PIPER_MODEL_DIR, then starts this script.
Exposes:
  GET  /health   — liveness / readiness (piper polls this after deploy)
  POST /predict  — returns the iris class and per-class probabilities

Example:
  curl -s http://localhost:9080/predict \
    -H 'Content-Type: application/json' \
    -d '{"features": [5.1, 3.5, 1.4, 0.2]}' | python3 -m json.tool
"""
import os
import pickle
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer
import json

MODEL_DIR = os.environ.get("PIPER_MODEL_DIR", ".")
PORT      = int(os.environ.get("PORT", "9080"))
CLASSES   = ["setosa", "versicolor", "virginica"]

model_path = os.path.join(MODEL_DIR, "model.pkl")
if not os.path.exists(model_path):
    print(f"ERROR: model not found at {model_path}", file=sys.stderr)
    sys.exit(1)

with open(model_path, "rb") as f:
    MODEL = pickle.load(f)

print(f"iris-predictor: model loaded from {model_path}")


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        pass  # silence access log; piper captures stdout/stderr separately

    def _send_json(self, code: int, payload: dict):
        body = json.dumps(payload).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/health":
            self._send_json(200, {"status": "ok", "model": model_path})
        else:
            self._send_json(404, {"error": "not found"})

    def do_POST(self):
        if self.path != "/predict":
            self._send_json(404, {"error": "not found"})
            return

        length = int(self.headers.get("Content-Length", 0))
        body   = self.rfile.read(length)
        try:
            req = json.loads(body)
        except json.JSONDecodeError as exc:
            self._send_json(400, {"error": f"invalid JSON: {exc}"})
            return

        features = req.get("features")
        if not features or len(features) != 4:
            self._send_json(400, {"error": "expected features: [sepal_length, sepal_width, petal_length, petal_width]"})
            return

        pred  = MODEL.predict([features])[0]
        proba = MODEL.predict_proba([features])[0].tolist()
        self._send_json(200, {
            "prediction":  CLASSES[pred],
            "class_id":    int(pred),
            "probability": {CLASSES[i]: round(p, 4) for i, p in enumerate(proba)},
        })


if __name__ == "__main__":
    print(f"iris-predictor listening on :{PORT}", flush=True)
    HTTPServer(("0.0.0.0", PORT), Handler).serve_forever()
