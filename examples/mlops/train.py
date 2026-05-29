"""
Iris classifier training script.

Reads hyperparameters from environment variables (injected via pipeline YAML env:).
Writes model.pkl and metrics.json to PIPER_OUTPUT_DIR.
Emits PIPER_METRIC lines so piper stores results in the run_metrics table.
"""
import json
import os
import pickle
import sys

from sklearn.datasets import load_iris
from sklearn.ensemble import RandomForestClassifier
from sklearn.metrics import accuracy_score, f1_score
from sklearn.model_selection import train_test_split

output_dir  = os.environ.get("PIPER_OUTPUT_DIR", ".")
n_estimators = int(os.environ.get("N_ESTIMATORS", "100"))
test_size    = float(os.environ.get("TEST_SIZE", "0.2"))
random_state = int(os.environ.get("RANDOM_STATE", "42"))

os.makedirs(output_dir, exist_ok=True)
print(f"output_dir={output_dir}  n_estimators={n_estimators}  test_size={test_size}")

# ── Load data ────────────────────────────────────────────────────────────────
iris = load_iris()
X, y = iris.data, iris.target

X_train, X_test, y_train, y_test = train_test_split(
    X, y, test_size=test_size, random_state=random_state, stratify=y
)
print(f"train={len(X_train)}  test={len(X_test)}")

# ── Train ────────────────────────────────────────────────────────────────────
clf = RandomForestClassifier(n_estimators=n_estimators, random_state=random_state)
clf.fit(X_train, y_train)

# ── Evaluate ─────────────────────────────────────────────────────────────────
y_pred   = clf.predict(X_test)
accuracy = accuracy_score(y_test, y_pred)
f1       = f1_score(y_test, y_pred, average="weighted")

# Piper reads lines starting with PIPER_METRIC and stores key=value in DB
print(f"PIPER_METRIC accuracy={accuracy:.4f}")
print(f"PIPER_METRIC f1={f1:.4f}")
print(f"PIPER_METRIC n_estimators={n_estimators}")
print(f"accuracy={accuracy:.4f}  f1={f1:.4f}", flush=True)

# ── Save artifacts ───────────────────────────────────────────────────────────
model_path   = os.path.join(output_dir, "model.pkl")
metrics_path = os.path.join(output_dir, "metrics.json")

with open(model_path, "wb") as f:
    pickle.dump(clf, f)

with open(metrics_path, "w") as f:
    json.dump(
        {"accuracy": accuracy, "f1": f1, "n_estimators": n_estimators, "test_size": test_size},
        f, indent=2,
    )

print(f"model  → {model_path}")
print(f"metrics → {metrics_path}")
