package dockerdriver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/pkg/pipeline"
	pipelinedriver "github.com/loykin/piper/pkg/pipeline/pipelinedriver"
	"github.com/loykin/piper/pkg/pipeline/worker/agent"
)

func TestDockerRuntimeE2E_CommandCompletes(t *testing.T) {
	image := os.Getenv("PIPER_PIPELINE_DOCKER_E2E_IMAGE")
	if image == "" {
		t.Skip("set PIPER_PIPELINE_DOCKER_E2E_IMAGE to run Docker pipeline e2e")
	}

	outputDir := t.TempDir()
	d, err := New(Config{RuntimeID: "docker-e2e", ResultDir: filepath.Join(outputDir, ".results")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	step := pipeline.Step{Name: "proof", Run: pipeline.Run{Command: []string{"sh", "-c", "echo docker-e2e > $PIPER_OUTPUT_DIR/proof.txt"}}}
	stepJSON, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}
	task := &proto.Task{
		ProjectID: "default",
		ID:        "docker-e2e-run:proof",
		RunID:     "docker-e2e-run",
		StepName:  "proof",
		Step:      stepJSON,
		Attempt:   1,
		CreatedAt: time.Now().UTC(),
	}
	handle, err := d.Start(context.Background(), task, pipelinedriver.ExecSpec{
		RuntimeKey: "docker-e2e-run-proof-a1",
		Image:      image,
		OutputDir:  outputDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	exit, err := d.Wait(context.Background(), handle)
	if err != nil {
		t.Fatal(err)
	}
	if exit.InfraFailure != nil {
		t.Fatalf("container infrastructure failure: %v", exit.InfraFailure)
	}
	data, err := os.ReadFile(exit.ResultPath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := agent.ReadAgentResult(data)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "done" {
		t.Fatalf("task status = %q, error = %q", result.Status, result.Error)
	}
	proof, err := os.ReadFile(filepath.Join(outputDir, task.RunID, step.Name, "proof.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(proof) != "docker-e2e\n" {
		t.Fatalf("proof output = %q", proof)
	}
}
