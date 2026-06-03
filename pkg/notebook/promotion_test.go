package notebook

import (
	"strings"
	"testing"
)

func TestBuildPromotionDraft(t *testing.T) {
	nb := &NotebookServer{
		Name:     "demo",
		Env:      "conda:ml",
		Image:    "jupyter/scipy-notebook:latest",
		WorkDir:  "/tmp/work",
		WorkerID: "worker-1",
		Endpoint: "tunnel://worker-1?target=127.0.0.1:8888",
		YAML: `metadata:
  name: demo
spec:
  process:
    env: conda:ml
`,
	}
	draft := BuildPromotionDraft(nb, PromotionTargetRepo)
	if !strings.Contains(draft, "target: repo") {
		t.Fatalf("draft missing target: %s", draft)
	}
	if !strings.Contains(draft, "notebook_name: demo") {
		t.Fatalf("draft missing notebook_name: %s", draft)
	}
	if !strings.Contains(draft, "endpoint: tunnel://worker-1?target=127.0.0.1:8888") {
		t.Fatalf("draft missing endpoint: %s", draft)
	}
}

func TestValidatePromotion(t *testing.T) {
	nb := &NotebookServer{
		Name: "demo",
		YAML: `metadata:
  name: demo
spec:
  prepare:
    steps:
      - type: command
        backend: process
        command: ["echo", "hello"]
`,
	}
	validation := ValidatePromotion(nb, PromotionTargetObjectStore, false)
	if validation.Status != "error" {
		t.Fatalf("validation status = %q, want error", validation.Status)
	}
	if len(validation.Messages) == 0 {
		t.Fatal("expected validation messages")
	}

	ok := ValidatePromotion(nb, PromotionTargetRepo, true)
	if ok.Status != "ok" && ok.Status != "warning" {
		t.Fatalf("validation status = %q, want ok or warning", ok.Status)
	}
}

func TestParsePromotionTarget(t *testing.T) {
	if got := ParsePromotionTarget(""); got != PromotionTargetDraft {
		t.Fatalf("empty target = %q, want draft", got)
	}
	if got := ParsePromotionTarget("repo"); got != PromotionTargetRepo {
		t.Fatalf("repo target = %q, want repo", got)
	}
	if got := ParsePromotionTarget("unsupported"); got != PromotionTarget("unsupported") {
		t.Fatalf("unsupported target = %q, want original", got)
	}
}
