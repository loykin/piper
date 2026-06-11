import json
import os

from support.summary_utils import summarize


with open(
    os.path.join(os.environ["PIPER_INPUT_DIR"], "features", "features.json"),
    encoding="utf-8",
) as src:
    features = json.load(src)

summary = summarize(features["values"])
summary["source"] = "frontend-template"

with open(
    os.path.join(os.environ["PIPER_OUTPUT_DIR"], "summary.json"),
    "w",
    encoding="utf-8",
) as dst:
    json.dump(summary, dst)
