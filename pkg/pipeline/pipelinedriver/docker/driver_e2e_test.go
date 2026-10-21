package dockerdriver

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/pkg/pipeline"
	pipelinedriver "github.com/loykin/piper/pkg/pipeline/pipelinedriver"
)

func TestDockerRuntimeE2E_CommandCompletes(t *testing.T) {
	image := os.Getenv("PIPER_PIPELINE_DOCKER_E2E_IMAGE")
	if image == "" {
		t.Skip("set PIPER_PIPELINE_DOCKER_E2E_IMAGE to run Docker pipeline e2e")
	}

	d, err := New(Config{RuntimeID: "docker-e2e"})
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
	// The result is copied out of the container; no host directory is mounted,
	// so the step's write to $PIPER_OUTPUT_DIR proves the copied-in root works.
	if exit.Result == nil {
		t.Fatal("Wait returned no result")
	}
	if exit.Result.Status != "done" {
		t.Fatalf("task status = %q, error = %q", exit.Result.Status, exit.Result.Error)
	}
}
