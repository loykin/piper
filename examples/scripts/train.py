import os
import json

input_dir = os.environ.get("PIPER_INPUT_DIR", ".")
output_dir = os.environ.get("PIPER_OUTPUT_DIR", ".")
run_id = os.environ.get("PIPER_RUN_ID", "local")

os.makedirs(output_dir, exist_ok=True)

# Parameters
epochs = int(os.environ.get("PIPER_PARAM_epochs", 3))
lr = float(os.environ.get("PIPER_PARAM_lr", 0.01))

print(f"[train] run_id={run_id} epochs={epochs} lr={lr}")

# Simple training simulation
losses = []
for epoch in range(1, epochs + 1):
    loss = 1.0 / (epoch * lr * 10 + 1)
    losses.append({"epoch": epoch, "loss": round(loss, 4)})
    print(f"  epoch {epoch}/{epochs}  loss={loss:.4f}")

result = {"run_id": run_id, "epochs": epochs, "lr": lr, "losses": losses}
out_path = os.path.join(output_dir, "result.json")
with open(out_path, "w") as f:
    json.dump(result, f, indent=2)

print(f"[train] saved → {out_path}")
