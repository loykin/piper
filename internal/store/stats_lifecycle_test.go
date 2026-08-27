package store

import (
	"context"
	"testing"
	"time"

	"github.com/loykin/piper/internal/logstore"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
)

func TestDeleteRunPreservesLogsAndMetrics(t *testing.T) {
	ctx := context.Background()
	repos, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repos.Close() })

	const projectID = "stats-lifecycle-project"
	const runID = "run-1"
	if err := repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	if err := repos.Run.Create(ctx, &run.Run{ID: runID, ProjectID: projectID, PipelineName: "pipeline", Status: run.StatusSuccess, StartedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := repos.Log.Append(ctx, []*logstore.Line{{ProjectID: projectID, RunID: runID, StepName: "step", Ts: time.Now().UTC(), Stream: "stdout", Line: "kept"}}); err != nil {
		t.Fatal(err)
	}
	if err := repos.Metric.AppendMetrics(ctx, []*logstore.Metric{{ProjectID: projectID, RunID: runID, StepName: "step", Key: "loss", Value: 0.5, Ts: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}

	if err := repos.DeleteRun(ctx, projectID, runID); err != nil {
		t.Fatal(err)
	}
	if got, err := repos.Run.Get(ctx, projectID, runID); err != nil || got != nil {
		t.Fatalf("run still exists: got=%#v err=%v", got, err)
	}
	logs, err := repos.Log.Query(projectID, runID, "step", 0)
	if err != nil || len(logs) != 1 || logs[0].Line != "kept" {
		t.Fatalf("logs were not preserved: logs=%#v err=%v", logs, err)
	}
	metrics, err := repos.Metric.QueryMetrics(projectID, runID, "step")
	if err != nil || len(metrics) != 1 || metrics[0].Key != "loss" {
		t.Fatalf("metrics were not preserved: metrics=%#v err=%v", metrics, err)
	}
}
