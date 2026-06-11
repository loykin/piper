package template

import (
	"testing"

	"github.com/piper/piper/pkg/pipeline"
)

func TestRewriteLocalSourcesSkipsPureCommandSteps(t *testing.T) {
	input := `
metadata:
  name: original
spec:
  steps:
    - name: prepare
      run:
        type: command
        command: ["echo", "ready"]
    - name: train
      run:
        type: python
        source: local
        path: scripts/train.py
        command: ["python3", "$PIPER_SCRIPT_PATH"]
    - name: evaluate
      run:
        type: notebook
        notebook: notebooks/evaluate.ipynb
`

	rewritten := rewriteLocalSources(input, "snapshot-1", "template-name")
	pl, err := pipeline.Parse([]byte(rewritten))
	if err != nil {
		t.Fatalf("parse rewritten pipeline: %v", err)
	}

	if pl.Metadata.Name != "template-name" {
		t.Fatalf("metadata.name = %q, want template-name", pl.Metadata.Name)
	}

	prepare := pl.Spec.Steps[0].Run
	if prepare.Source != "" || prepare.SnapshotPrefix != "" {
		t.Fatalf("pure command source was rewritten: %+v", prepare)
	}

	for _, step := range pl.Spec.Steps[1:] {
		if step.Run.Source != "s3" {
			t.Errorf("step %q source = %q, want s3", step.Name, step.Run.Source)
		}
		if step.Run.SnapshotPrefix != "snapshots/snapshot-1/" {
			t.Errorf("step %q snapshot prefix = %q", step.Name, step.Run.SnapshotPrefix)
		}
	}
}
