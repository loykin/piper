import json
import os

from support.math_utils import scaled_values
from tinyrecord import record


input_path = os.path.join(
    os.environ["PIPER_INPUT_DIR"],
    "features",
    "features.json",
)
with open(input_path, encoding="utf-8") as src:
    features = json.load(src)

audit_path = os.path.join(
    os.environ["PIPER_INPUT_DIR"],
    "audit",
    "audit.json",
)
with open(audit_path, encoding="utf-8") as src:
    audit = json.load(src)
if audit != {"validated": True, "source": "command"}:
    raise RuntimeError(f"unexpected command audit output: {audit!r}")

expected = record(scaled_values(features["multiplier"]))
expected["multiplier"] = features["multiplier"]
if features != expected:
    raise RuntimeError(f"unexpected notebook output: {features!r}")

summary = {
    "count": features["count"],
    "sum": sum(features["values"]),
    "source": "notebook",
    "audit_source": audit["source"],
}
output_path = os.path.join(os.environ["PIPER_OUTPUT_DIR"], "summary.json")
with open(output_path, "w", encoding="utf-8") as dst:
    json.dump(summary, dst)

print(json.dumps(summary, sort_keys=True))
