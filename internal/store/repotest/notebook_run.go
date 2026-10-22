package repotest

import (
	"context"
	"testing"
	"time"

	"github.com/loykin/piper/pkg/notebook"
)

// NotebookRunSuite exercises notebook.Repository's run lifecycle: a run is
// recorded in notebook_history when it ends (FinishRun) as one row with its
// own start time — ending again after the runtime revived it updates that
// row — and Delete records nothing.
func NotebookRunSuite(t *testing.T, repo notebook.Repository, projectID string) {
	t.Helper()
	ctx := context.Background()

	nb := &notebook.NotebookServer{ProjectID: projectID, Name: "nb", Status: notebook.StatusRunning, Endpoint: "http://x", Token: "secret", PID: 7}
	if err := repo.Create(ctx, nb); err != nil {
		t.Fatal(err)
	}
	firstStart := nb.StartedAt

	// First run ends: recorded once, however often the end is reported.
	for i, want := range []bool{true, false} {
		finished, err := repo.FinishRun(ctx, projectID, "nb", notebook.StatusStopped, "")
		if err != nil || finished != want {
			t.Fatalf("FinishRun #%d = %v, %v; want %v", i, finished, err, want)
		}
	}
	got, _ := repo.Get(ctx, projectID, "nb")
	if got.Status != notebook.StatusStopped || got.Endpoint != "" || got.Token != "" || got.PID != 0 {
		t.Fatalf("notebook after FinishRun = %+v", got)
	}

	// Second run: StartRun stamps a new start, then it fails.
	time.Sleep(10 * time.Millisecond)
	if err := repo.StartRun(ctx, projectID, "nb"); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.Get(ctx, projectID, "nb")
	if got.Status != notebook.StatusStarting || !got.StartedAt.After(firstStart) {
		t.Fatalf("notebook after StartRun = %+v, want starting with a later started_at", got)
	}
	secondStart := got.StartedAt
	if finished, err := repo.FinishRun(ctx, projectID, "nb", notebook.StatusFailed, "back-off 10s"); err != nil || !finished {
		t.Fatalf("FinishRun(failed) = %v, %v", finished, err)
	}
	// The runtime revives the same run on its own (K8s restarting a crashed
	// container: failed -> starting, no StartRun) and it fails again. Still
	// one row for the run, holding the latest end.
	got, _ = repo.Get(ctx, projectID, "nb")
	got.Status = notebook.StatusStarting
	if err := repo.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	if finished, err := repo.FinishRun(ctx, projectID, "nb", notebook.StatusFailed, "exit 1"); err != nil || !finished {
		t.Fatalf("FinishRun(failed again) = %v, %v", finished, err)
	}

	// Deleting the ended notebook records nothing more.
	if err := repo.Delete(ctx, projectID, "nb"); err != nil {
		t.Fatal(err)
	}
	hist, err := repo.ListHistory(ctx, projectID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 {
		t.Fatalf("history = %+v, want 2 runs", hist)
	}
	// Most recently stopped first. Compare at microsecond precision (postgres).
	same := func(a, b time.Time) bool { return a.Sub(b).Abs() < time.Millisecond }
	if hist[0].Status != notebook.StatusFailed || hist[0].Reason != "exit 1" || !same(hist[0].DeployedAt, secondStart) {
		t.Fatalf("second run = %+v, want failed/exit 1 deployed at %v", hist[0], secondStart)
	}
	if hist[1].Status != notebook.StatusStopped || !same(hist[1].DeployedAt, firstStart) || hist[1].Endpoint != "" {
		t.Fatalf("first run = %+v, want stopped deployed at %v", hist[1], firstStart)
	}
}
