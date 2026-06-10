package executor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/piper/piper/pkg/pipeline"
)

func TestRunPrepareRunsSequentiallyInStepWorkDir(t *testing.T) {
	workDir := t.TempDir()
	step := &pipeline.Step{
		Name: "prepare",
		Run: pipeline.Run{Prepare: [][]string{
			{"sh", "-c", "printf first > order.txt"},
			{"sh", "-c", "printf second >> order.txt"},
		}},
	}

	if err := runPrepare(context.Background(), step, ExecConfig{}, workDir, nil, nil); err != nil {
		t.Fatalf("runPrepare() error: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(workDir, "order.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "firstsecond" {
		t.Fatalf("order = %q, want firstsecond", got)
	}
}

func TestRunPrepareStopsBeforeLaterCommands(t *testing.T) {
	workDir := t.TempDir()
	step := &pipeline.Step{
		Name: "prepare",
		Run: pipeline.Run{Prepare: [][]string{
			{"sh", "-c", "exit 7"},
			{"sh", "-c", "touch should-not-exist"},
		}},
	}

	if err := runPrepare(context.Background(), step, ExecConfig{}, workDir, nil, nil); err == nil {
		t.Fatal("runPrepare() expected error")
	}
	if _, err := os.Stat(filepath.Join(workDir, "should-not-exist")); !os.IsNotExist(err) {
		t.Fatalf("later prepare command ran: %v", err)
	}
}
