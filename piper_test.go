package piper

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/piper/piper/pkg/pipeline"
)

func TestRunPipeline_localArtifactPathIncludesRunID(t *testing.T) {
	outputDir := t.TempDir()
	p, err := New(Config{OutputDir: outputDir})
	if err != nil {
		t.Fatal(err)
	}

	pl := &pipeline.Pipeline{
		Metadata: pipeline.Metadata{Name: "local-path-test"},
		Spec: pipeline.Spec{Steps: []pipeline.Step{{
			Name: "train",
			Run: pipeline.Run{
				Command: []string{"sh", "-c", "echo artifact > $PIPER_OUTPUT_DIR/result.txt"},
			},
		}}},
	}

	res, err := p.runPipelineWithRunID(context.Background(), pl, "run-local", RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed() {
		t.Fatalf("pipeline failed: %+v", res.Steps["train"])
	}

	expected := filepath.Join(outputDir, "run-local", "train", "result.txt")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("expected artifact at %s: %v", expected, err)
	}

	oldLayout := filepath.Join(outputDir, "train", "result.txt")
	if _, err := os.Stat(oldLayout); !os.IsNotExist(err) {
		t.Fatalf("old artifact layout should not exist at %s", oldLayout)
	}
}
