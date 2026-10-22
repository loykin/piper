package notebook

import (
	"context"
	"testing"
)

// A run is recorded in history when it ends — stopped or failed — and only
// once, however many times the end is reported.
func TestStatusSink_RecordsRunWhenItEnds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		report StatusReport
	}{
		{"stopped", StatusReport{Status: StatusStopped}},
		{"failed", StatusReport{Status: StatusFailed, Reason: "exit 1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeRepo()
			sink := NewStatusSink(repo, newFakeVols())
			ctx := context.Background()
			_ = repo.Create(ctx, &NotebookServer{Name: "nb", Status: StatusRunning, Endpoint: "http://x", Token: "t", PID: 7})

			for i := 0; i < 2; i++ {
				if err := sink.Update(ctx, "project-a", "", "nb", tc.report); err != nil {
					t.Fatalf("Update #%d: %v", i, err)
				}
			}

			history := repo.historySnapshot()
			if len(history) != 1 {
				t.Fatalf("history = %+v, want exactly one row", history)
			}
			if history[0].Status != tc.report.Status || history[0].Reason != tc.report.Reason {
				t.Fatalf("history row = %+v, want status %q reason %q", history[0], tc.report.Status, tc.report.Reason)
			}
			nb, _ := repo.Get(ctx, "project-a", "nb")
			if nb.Status != tc.report.Status || nb.Endpoint != "" || nb.Token != "" || nb.PID != 0 {
				t.Fatalf("notebook = %+v, want ended with connection fields cleared", nb)
			}
		})
	}
}

// Running and starting reports do not end a run and record nothing.
func TestStatusSink_NonTerminalReportsRecordNothing(t *testing.T) {
	repo := newFakeRepo()
	sink := NewStatusSink(repo, newFakeVols())
	ctx := context.Background()
	_ = repo.Create(ctx, &NotebookServer{Name: "nb", Status: StatusStarting})

	if err := sink.Update(ctx, "project-a", "", "nb", StatusReport{Status: StatusRunning, Endpoint: "http://x"}); err != nil {
		t.Fatal(err)
	}
	if history := repo.historySnapshot(); len(history) != 0 {
		t.Fatalf("history = %+v, want none", history)
	}
}

// Stop then the driver's stopped report: one run, recorded once; deleting the
// stopped notebook afterwards records nothing more.
func TestManager_StopThenDeleteRecordsOneRun(t *testing.T) {
	repo := newFakeRepo()
	drv := newFakeDriver()
	m := New(repo, newFakeVols(), drv)
	ctx := context.Background()
	_ = repo.Create(ctx, &NotebookServer{Name: "nb", Status: StatusRunning})

	if err := m.Stop(ctx, "project-a", "nb"); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateStatus(ctx, "project-a", "", "nb", StatusReport{Status: StatusStopped}); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, "project-a", "nb"); err != nil {
		t.Fatal(err)
	}
	history := repo.historySnapshot()
	if len(history) != 1 || history[0].Status != StatusStopped {
		t.Fatalf("history = %+v, want one stopped run", history)
	}
}

// K8s restarts a crashed notebook container on its own, so the observed status
// goes failed -> starting -> failed within one run. History keeps one row for
// that run, ending in its latest state; deleting it updates the same row.
func TestStatusSink_CrashLoopKeepsOneRowPerRun(t *testing.T) {
	repo := newFakeRepo()
	drv := newFakeDriver()
	m := New(repo, newFakeVols(), drv)
	ctx := context.Background()
	_ = repo.Create(ctx, &NotebookServer{Name: "nb", Status: StatusStarting})

	for _, r := range []StatusReport{
		{Status: StatusFailed, Reason: "back-off 10s"},
		{Status: StatusStarting},
		{Status: StatusFailed, Reason: "back-off 20s"},
		{Status: StatusStarting},
	} {
		if err := m.UpdateStatus(ctx, "project-a", "", "nb", r); err != nil {
			t.Fatal(err)
		}
	}
	if history := repo.historySnapshot(); len(history) != 1 || history[0].Reason != "back-off 20s" {
		t.Fatalf("history = %+v, want one row with the latest failure", history)
	}
	if err := m.Delete(ctx, "project-a", "nb"); err != nil {
		t.Fatal(err)
	}
	if history := repo.historySnapshot(); len(history) != 1 || history[0].Status != StatusStopped {
		t.Fatalf("history = %+v, want the run's one row, now stopped", history)
	}
}
