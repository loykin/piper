// Package repotest provides shared contract tests for Repository implementations.
// Use RunRepoSuite and StepRepoSuite from both SQLite and PostgreSQL test packages
// to ensure both drivers satisfy the same behavioral contract.
package repotest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/loykin/piper/pkg/credential"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
)

func ProjectRepoSuite(t *testing.T, repo project.Repository) {
	t.Helper()
	ctx := context.Background()

	first := &project.Project{ID: uuid.NewString(), Name: "alpha", Description: "first"}
	second := &project.Project{ID: uuid.NewString(), Name: "beta", Description: "second"}
	if err := repo.Create(ctx, first); err != nil {
		t.Fatalf("Create first project: %v", err)
	}
	if err := repo.Create(ctx, second); err != nil {
		t.Fatalf("Create second project: %v", err)
	}

	got, err := repo.Get(ctx, first.ID)
	if err != nil {
		t.Fatalf("Get project: %v", err)
	}
	if got == nil || got.ID != first.ID || got.Name != first.Name {
		t.Fatalf("Get project = %#v, want %#v", got, first)
	}

	projects, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List projects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("List projects = %d, want 2", len(projects))
	}

	if err := repo.Delete(ctx, first.ID); err != nil {
		t.Fatalf("Delete project: %v", err)
	}
	deleted, err := repo.Get(ctx, first.ID)
	if err != nil {
		t.Fatalf("Get deleted project: %v", err)
	}
	if deleted != nil {
		t.Fatalf("deleted project still exists: %#v", deleted)
	}
}

func CredentialRepoSuite(t *testing.T, repo credential.Repository, projectID string) {
	t.Helper()
	ctx := context.Background()

	t.Run("Create_rotate_and_get_metadata", func(t *testing.T) {
		meta := &credential.Metadata{
			ProjectID: projectID,
			Name:      "github",
			Kind:      credential.KindGeneric,
			Keys:      []string{"token"},
		}
		if err := repo.Create(ctx, meta, []byte("old")); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := repo.Rotate(ctx, projectID, "github", []byte("new"), []string{"token", "user"}); err != nil {
			t.Fatalf("Rotate: %v", err)
		}
		got, err := repo.Get(ctx, projectID, "github")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got == nil || got.Kind != credential.KindGeneric || len(got.Keys) != 2 {
			t.Fatalf("credential metadata = %#v, want generic with 2 keys", got)
		}
		value, err := repo.GetValue(ctx, projectID, "github")
		if err != nil {
			t.Fatalf("GetValue: %v", err)
		}
		if string(value) != "new" {
			t.Fatalf("active value = %q, want new", string(value))
		}
	})
}

// RunRepoSuite runs a full CRUD contract test against any run.Repository implementation.
func RunRepoSuite(t *testing.T, repo run.Repository, projectID string) {
	t.Helper()
	ctx := context.Background()

	t.Run("Create_and_Get", func(t *testing.T) {
		r := &run.Run{
			ID:           uuid.NewString(),
			ProjectID:    projectID,
			PipelineName: "test-pipeline",
			Status:       run.StatusRunning,
			StartedAt:    time.Now().UTC().Truncate(time.Millisecond),
		}
		if err := repo.Create(ctx, r); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := repo.Get(ctx, projectID, r.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got == nil {
			t.Fatal("Get returned nil")
		}
		if got.ID != r.ID {
			t.Errorf("ID mismatch: got %q want %q", got.ID, r.ID)
		}
		if got.PipelineName != r.PipelineName {
			t.Errorf("PipelineName mismatch: got %q want %q", got.PipelineName, r.PipelineName)
		}
		if got.Status != r.Status {
			t.Errorf("Status mismatch: got %q want %q", got.Status, r.Status)
		}
	})

	t.Run("Get_missing_returns_not_found", func(t *testing.T) {
		// Get for a missing ID should either return (nil, nil) or (nil, sql.ErrNoRows).
		// Both are acceptable; the caller must check both cases.
		got, _ := repo.Get(ctx, projectID, "nonexistent-id")
		if got != nil {
			t.Errorf("expected nil record for missing run, got %+v", got)
		}
	})

	t.Run("List_by_status", func(t *testing.T) {
		id := uuid.NewString()
		if err := repo.Create(ctx, &run.Run{
			ID:           id,
			ProjectID:    projectID,
			PipelineName: "list-test",
			Status:       run.StatusFailed,
			StartedAt:    time.Now().UTC(),
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		runs, err := repo.List(ctx, projectID, run.RunFilter{Status: run.StatusFailed})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		found := false
		for _, r := range runs {
			if r.ID == id {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("created run %q not found in List by status=failed", id)
		}
	})

	t.Run("UpdateStatus", func(t *testing.T) {
		id := uuid.NewString()
		if err := repo.Create(ctx, &run.Run{
			ID:           id,
			ProjectID:    projectID,
			PipelineName: "update-test",
			Status:       run.StatusRunning,
			StartedAt:    time.Now().UTC(),
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		if err := repo.UpdateStatus(ctx, projectID, id, run.StatusSuccess, &now); err != nil {
			t.Fatalf("UpdateStatus: %v", err)
		}
		got, err := repo.Get(ctx, projectID, id)
		if err != nil || got == nil {
			t.Fatalf("Get after UpdateStatus: %v, got=%v", err, got)
		}
		if got.Status != run.StatusSuccess {
			t.Errorf("status mismatch: got %q want %q", got.Status, run.StatusSuccess)
		}
	})

	t.Run("FinalizeStatusCAS", func(t *testing.T) {
		id := uuid.NewString()
		if err := repo.Create(ctx, &run.Run{
			ID:           id,
			ProjectID:    projectID,
			PipelineName: "finalize-cas-test",
			Status:       run.StatusRunning,
			StartedAt:    time.Now().UTC(),
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		applied, err := repo.FinalizeStatusCAS(ctx, projectID, id, run.StatusSuccess, &now)
		if err != nil {
			t.Fatalf("FinalizeStatusCAS: %v", err)
		}
		if !applied {
			t.Fatal("FinalizeStatusCAS on a non-terminal row = false, want true")
		}
		got, err := repo.Get(ctx, projectID, id)
		if err != nil || got == nil {
			t.Fatalf("Get after FinalizeStatusCAS: %v, got=%v", err, got)
		}
		if got.Status != run.StatusSuccess {
			t.Errorf("status mismatch: got %q want %q", got.Status, run.StatusSuccess)
		}

		// A second finalize attempt (e.g. a delayed/duplicate write racing the
		// first) must not clobber the already-terminal row.
		later := now.Add(time.Minute)
		applied, err = repo.FinalizeStatusCAS(ctx, projectID, id, run.StatusFailed, &later)
		if err != nil {
			t.Fatalf("FinalizeStatusCAS on already-terminal row: %v", err)
		}
		if applied {
			t.Fatal("FinalizeStatusCAS on an already-terminal row = true, want false")
		}
		got, err = repo.Get(ctx, projectID, id)
		if err != nil || got == nil {
			t.Fatalf("Get after second FinalizeStatusCAS: %v, got=%v", err, got)
		}
		if got.Status != run.StatusSuccess {
			t.Errorf("status changed by a losing CAS: got %q want %q", got.Status, run.StatusSuccess)
		}
	})

	t.Run("SetWorkerID", func(t *testing.T) {
		id := uuid.NewString()
		if err := repo.Create(ctx, &run.Run{
			ID:           id,
			ProjectID:    projectID,
			PipelineName: "set-worker-id-test",
			Status:       run.StatusRunning,
			StartedAt:    time.Now().UTC(),
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		applied, err := repo.SetWorkerID(ctx, projectID, id, "worker-a")
		if err != nil {
			t.Fatalf("SetWorkerID: %v", err)
		}
		if !applied {
			t.Fatal("SetWorkerID on an unbound run applied = false, want true")
		}
		got, err := repo.Get(ctx, projectID, id)
		if err != nil || got == nil {
			t.Fatalf("Get after SetWorkerID: %v, got=%v", err, got)
		}
		if got.WorkerID != "worker-a" {
			t.Errorf("WorkerID = %q, want %q", got.WorkerID, "worker-a")
		}

		// A second call must not reassign an already-bound run to a
		// different worker (CAS on worker_id='', not a blind overwrite) —
		// and must report that it didn't, not merely leave the row alone.
		applied, err = repo.SetWorkerID(ctx, projectID, id, "worker-b")
		if err != nil {
			t.Fatalf("SetWorkerID (second call): %v", err)
		}
		if applied {
			t.Fatal("SetWorkerID on an already-bound run applied = true, want false")
		}
		got, err = repo.Get(ctx, projectID, id)
		if err != nil || got == nil {
			t.Fatalf("Get after second SetWorkerID: %v, got=%v", err, got)
		}
		if got.WorkerID != "worker-a" {
			t.Errorf("WorkerID changed by a second SetWorkerID call: got %q, want %q (unchanged)", got.WorkerID, "worker-a")
		}
	})

	t.Run("TouchWorkerLastSeen", func(t *testing.T) {
		id := uuid.NewString()
		if err := repo.Create(ctx, &run.Run{
			ID:           id,
			ProjectID:    projectID,
			PipelineName: "touch-worker-last-seen-test",
			Status:       run.StatusRunning,
			StartedAt:    time.Now().UTC(),
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := repo.Get(ctx, projectID, id)
		if err != nil || got == nil {
			t.Fatalf("Get before binding: %v, got=%v", err, got)
		}
		if got.WorkerLastSeenAt != nil {
			t.Fatalf("WorkerLastSeenAt = %v on a fresh run, want nil", got.WorkerLastSeenAt)
		}
		if applied, err := repo.SetWorkerID(ctx, projectID, id, "worker-a"); err != nil || !applied {
			t.Fatalf("SetWorkerID: applied=%v err=%v", applied, err)
		}

		if err := repo.TouchWorkerLastSeen(ctx, "worker-a", []string{id, "nonexistent-run-id"}); err != nil {
			t.Fatalf("TouchWorkerLastSeen: %v", err)
		}
		got, err = repo.Get(ctx, projectID, id)
		if err != nil || got == nil {
			t.Fatalf("Get after TouchWorkerLastSeen: %v, got=%v", err, got)
		}
		if got.WorkerLastSeenAt == nil {
			t.Fatal("WorkerLastSeenAt still nil after TouchWorkerLastSeen")
		}
		first := *got.WorkerLastSeenAt

		// A heartbeat from a worker that isn't the run's bound owner must not
		// touch the row — otherwise a stale/mismatched worker could keep a
		// run's liveness timestamp fresh after it was rebound elsewhere.
		if err := repo.TouchWorkerLastSeen(ctx, "worker-b", []string{id}); err != nil {
			t.Fatalf("TouchWorkerLastSeen (wrong worker): %v", err)
		}
		got, err = repo.Get(ctx, projectID, id)
		if err != nil || got == nil {
			t.Fatalf("Get after mismatched TouchWorkerLastSeen: %v, got=%v", err, got)
		}
		if got.WorkerLastSeenAt == nil || !got.WorkerLastSeenAt.Equal(first) {
			t.Errorf("WorkerLastSeenAt changed by a non-owning worker's heartbeat: got %v, want unchanged %v", got.WorkerLastSeenAt, first)
		}
	})

	t.Run("SetCancelRequested", func(t *testing.T) {
		id := uuid.NewString()
		if err := repo.Create(ctx, &run.Run{
			ID:           id,
			ProjectID:    projectID,
			PipelineName: "set-cancel-requested-test",
			Status:       run.StatusRunning,
			StartedAt:    time.Now().UTC(),
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		applied, err := repo.SetCancelRequested(ctx, projectID, id)
		if err != nil {
			t.Fatalf("SetCancelRequested: %v", err)
		}
		if !applied {
			t.Fatal("SetCancelRequested on a fresh run applied = false, want true")
		}
		got, err := repo.Get(ctx, projectID, id)
		if err != nil || got == nil {
			t.Fatalf("Get after SetCancelRequested: %v, got=%v", err, got)
		}
		if got.CancelRequestedAt == nil {
			t.Fatal("CancelRequestedAt still nil after SetCancelRequested")
		}
		first := *got.CancelRequestedAt

		// A second call must not reset an already-pending request's
		// timestamp (CAS on IS NULL, not a blind overwrite).
		applied, err = repo.SetCancelRequested(ctx, projectID, id)
		if err != nil {
			t.Fatalf("SetCancelRequested (second call): %v", err)
		}
		if applied {
			t.Fatal("SetCancelRequested on an already-pending run applied = true, want false")
		}
		got, err = repo.Get(ctx, projectID, id)
		if err != nil || got == nil {
			t.Fatalf("Get after second SetCancelRequested: %v, got=%v", err, got)
		}
		if got.CancelRequestedAt == nil || !got.CancelRequestedAt.Equal(first) {
			t.Errorf("CancelRequestedAt changed by a second call: got %v, want unchanged %v", got.CancelRequestedAt, first)
		}
	})

	t.Run("ListTerminalBefore", func(t *testing.T) {
		pname := "terminal-before-" + uuid.NewString()
		now := time.Now().UTC()
		mk := func(id, status string, endedAt *time.Time) {
			if err := repo.Create(ctx, &run.Run{
				ID:           id,
				ProjectID:    projectID,
				PipelineName: pname,
				Status:       status,
				StartedAt:    now,
			}); err != nil {
				t.Fatalf("Create %s: %v", id, err)
			}
			if endedAt != nil {
				if err := repo.UpdateStatus(ctx, projectID, id, status, endedAt); err != nil {
					t.Fatalf("UpdateStatus %s: %v", id, err)
				}
			}
		}
		expired := now.Add(-2 * time.Hour)
		fresh := now.Add(-10 * time.Minute)
		mk(uuid.NewString(), run.StatusSuccess, &expired) // expired, terminal -> should match
		mk(uuid.NewString(), run.StatusSuccess, &fresh)   // not expired -> should not match
		mk(uuid.NewString(), run.StatusRunning, nil)      // non-terminal, no EndedAt -> should not match

		got, err := repo.ListTerminalBefore(ctx, projectID, now.Add(-time.Hour))
		if err != nil {
			t.Fatalf("ListTerminalBefore: %v", err)
		}
		var matched int
		for _, r := range got {
			if r.PipelineName == pname {
				matched++
				if r.Status == run.StatusRunning || r.Status == run.StatusScheduled {
					t.Errorf("ListTerminalBefore returned a non-terminal run: %+v", r)
				}
				if r.EndedAt == nil || !r.EndedAt.Before(now.Add(-time.Hour)) {
					t.Errorf("ListTerminalBefore returned a run not before cutoff: %+v", r)
				}
			}
		}
		if matched != 1 {
			t.Errorf("ListTerminalBefore matched %d runs for %s, want 1", matched, pname)
		}
	})

	t.Run("GetLatestSuccessful", func(t *testing.T) {
		pname := "pipeline-" + uuid.NewString()
		now := time.Now().UTC()
		for i, status := range []string{run.StatusFailed, run.StatusSuccess, run.StatusSuccess} {
			endedAt := now.Add(time.Duration(i) * time.Second)
			if err := repo.Create(ctx, &run.Run{
				ID:           uuid.NewString(),
				ProjectID:    projectID,
				PipelineName: pname,
				Status:       status,
				StartedAt:    now.Add(time.Duration(i) * time.Second),
				EndedAt:      &endedAt,
			}); err != nil {
				t.Fatalf("Create: %v", err)
			}
		}
		got, err := repo.GetLatestSuccessful(ctx, projectID, pname)
		if err != nil {
			t.Fatalf("GetLatestSuccessful: %v", err)
		}
		if got == nil {
			t.Fatal("expected a successful run, got nil")
		}
		if got.Status != run.StatusSuccess {
			t.Errorf("expected success status, got %q", got.Status)
		}

		// Non-existent pipeline should return nil, nil
		missing, err := repo.GetLatestSuccessful(ctx, projectID, "no-such-pipeline")
		if err != nil {
			t.Fatalf("GetLatestSuccessful(missing): %v", err)
		}
		if missing != nil {
			t.Errorf("expected nil for missing pipeline, got %+v", missing)
		}
	})

	t.Run("List_pagination_and_Count", func(t *testing.T) {
		pname := "pagination-" + uuid.NewString()
		now := time.Now().UTC()
		const total = 5
		for i := 0; i < total; i++ {
			startedAt := now.Add(time.Duration(i) * time.Second)
			if err := repo.Create(ctx, &run.Run{
				ID:           uuid.NewString(),
				ProjectID:    projectID,
				PipelineName: pname,
				Status:       run.StatusSuccess,
				StartedAt:    startedAt,
			}); err != nil {
				t.Fatalf("Create: %v", err)
			}
		}

		count, err := repo.Count(ctx, projectID, run.RunFilter{PipelineName: pname})
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		if count != total {
			t.Errorf("Count = %d, want %d", count, total)
		}

		page1, err := repo.List(ctx, projectID, run.RunFilter{PipelineName: pname, Limit: 2, Offset: 0})
		if err != nil {
			t.Fatalf("List page1: %v", err)
		}
		if len(page1) != 2 {
			t.Fatalf("page1 len = %d, want 2", len(page1))
		}
		page2, err := repo.List(ctx, projectID, run.RunFilter{PipelineName: pname, Limit: 2, Offset: 2})
		if err != nil {
			t.Fatalf("List page2: %v", err)
		}
		if len(page2) != 2 {
			t.Fatalf("page2 len = %d, want 2", len(page2))
		}
		if page1[0].ID == page2[0].ID || page1[1].ID == page2[1].ID {
			t.Errorf("page1 and page2 overlap: page1=%v page2=%v", []string{page1[0].ID, page1[1].ID}, []string{page2[0].ID, page2[1].ID})
		}
		page3, err := repo.List(ctx, projectID, run.RunFilter{PipelineName: pname, Limit: 2, Offset: 4})
		if err != nil {
			t.Fatalf("List page3: %v", err)
		}
		if len(page3) != 1 {
			t.Fatalf("page3 (last, partial) len = %d, want 1", len(page3))
		}

		// Limit=0 must mean "no limit" — every existing caller relies on this.
		all, err := repo.List(ctx, projectID, run.RunFilter{PipelineName: pname})
		if err != nil {
			t.Fatalf("List all: %v", err)
		}
		if len(all) != total {
			t.Errorf("List with no Limit = %d rows, want %d (Limit:0 must not truncate)", len(all), total)
		}
	})

	t.Run("List_pagination_stable_with_tied_started_at", func(t *testing.T) {
		// Every row shares the exact same started_at, the only column the
		// default ORDER BY sorts on besides a tiebreaker. Without a unique
		// secondary sort key (id), the DB is free to return ties in any
		// order per query, so paging by offset can duplicate or skip rows
		// across pages even though nothing changed between calls.
		pname := "pagination-tie-" + uuid.NewString()
		tied := time.Now().UTC()
		const total = 6
		for i := 0; i < total; i++ {
			if err := repo.Create(ctx, &run.Run{
				ID:           uuid.NewString(),
				ProjectID:    projectID,
				PipelineName: pname,
				Status:       run.StatusSuccess,
				StartedAt:    tied,
			}); err != nil {
				t.Fatalf("Create: %v", err)
			}
		}

		seen := make(map[string]int)
		for page := 0; page*2 < total; page++ {
			rows, err := repo.List(ctx, projectID, run.RunFilter{PipelineName: pname, Limit: 2, Offset: page * 2})
			if err != nil {
				t.Fatalf("List page %d: %v", page, err)
			}
			for _, r := range rows {
				seen[r.ID]++
			}
		}
		if len(seen) != total {
			t.Errorf("saw %d distinct rows across pages, want %d (ties without a stable tiebreaker duplicate/skip rows): %v", len(seen), total, seen)
		}
		for id, n := range seen {
			if n != 1 {
				t.Errorf("row %s appeared on %d pages, want exactly 1", id, n)
			}
		}
	})

	t.Run("Delete", func(t *testing.T) {
		id := uuid.NewString()
		if err := repo.Create(ctx, &run.Run{
			ID:           id,
			ProjectID:    projectID,
			PipelineName: "delete-test",
			Status:       run.StatusSuccess,
			StartedAt:    time.Now().UTC(),
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := repo.Delete(ctx, projectID, id); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		// After deletion, Get should return nil record (error or nil).
		got, _ := repo.Get(ctx, projectID, id)
		if got != nil {
			t.Errorf("expected nil record after delete, got %+v", got)
		}
	})
}

// StepRepoSuite runs a contract test against any run.StepRepository implementation.
func StepRepoSuite(t *testing.T, repo run.StepRepository, projectID string) {
	t.Helper()
	ctx := context.Background()

	t.Run("Upsert_and_List", func(t *testing.T) {
		runID := uuid.NewString()
		step := &run.Step{
			ProjectID: projectID,
			RunID:     runID,
			StepName:  "train",
			Status:    "pending",
		}
		if err := repo.Upsert(ctx, step); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		steps, err := repo.List(ctx, projectID, runID)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(steps) != 1 {
			t.Fatalf("expected 1 step, got %d", len(steps))
		}
		if steps[0].StepName != "train" {
			t.Errorf("StepName mismatch: got %q want %q", steps[0].StepName, "train")
		}
	})

	t.Run("Upsert_and_List_with_timestamps", func(t *testing.T) {
		runID := uuid.NewString()
		// Reproduces a worker reporting a result over JSON RPC: unmarshaling
		// an RFC3339 timestamp with a numeric (non-"Z") offset yields a
		// time.Time in an unnamed fixed-offset zone whenever that offset
		// doesn't happen to match the host's local zone. Constructed
		// directly (rather than via time.Parse) so the repro doesn't depend
		// on — and can't accidentally match — the test host's local TZ.
		started := time.Date(2026, 8, 1, 18, 39, 0, 123456789, time.FixedZone("", 5*3600+30*60))
		ended := started.Add(90 * time.Second)
		step := &run.Step{
			ProjectID: projectID,
			RunID:     runID,
			StepName:  "reported",
			Status:    "success",
			StartedAt: &started,
			EndedAt:   &ended,
		}
		if err := repo.Upsert(ctx, step); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		steps, err := repo.List(ctx, projectID, runID)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(steps) != 1 {
			t.Fatalf("expected 1 step, got %d", len(steps))
		}
		if steps[0].StartedAt == nil || !steps[0].StartedAt.Equal(started) {
			t.Errorf("StartedAt = %v, want %v", steps[0].StartedAt, started)
		}
		if steps[0].EndedAt == nil || !steps[0].EndedAt.Equal(ended) {
			t.Errorf("EndedAt = %v, want %v", steps[0].EndedAt, ended)
		}
	})

	t.Run("Upsert_updates_existing", func(t *testing.T) {
		runID := uuid.NewString()
		step := &run.Step{ProjectID: projectID, RunID: runID, StepName: "eval", Status: "pending"}
		if err := repo.Upsert(ctx, step); err != nil {
			t.Fatalf("Upsert initial: %v", err)
		}
		step.Status = "done"
		if err := repo.Upsert(ctx, step); err != nil {
			t.Fatalf("Upsert update: %v", err)
		}
		steps, err := repo.List(ctx, projectID, runID)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(steps) != 1 {
			t.Fatalf("expected 1 step after upsert, got %d", len(steps))
		}
		if steps[0].Status != "done" {
			t.Errorf("status mismatch: got %q want %q", steps[0].Status, "done")
		}
	})

	t.Run("UpsertCAS", func(t *testing.T) {
		runID := uuid.NewString()
		step := &run.Step{ProjectID: projectID, RunID: runID, StepName: "cas", Status: "running", Attempts: 1}
		applied, err := repo.UpsertCAS(ctx, step)
		if err != nil {
			t.Fatalf("UpsertCAS insert: %v", err)
		}
		if !applied {
			t.Fatal("UpsertCAS insert = false, want true")
		}

		// A newer attempt's write must apply normally.
		newer := &run.Step{ProjectID: projectID, RunID: runID, StepName: "cas", Status: "done", Attempts: 2}
		applied, err = repo.UpsertCAS(ctx, newer)
		if err != nil {
			t.Fatalf("UpsertCAS newer attempt: %v", err)
		}
		if !applied {
			t.Fatal("UpsertCAS newer attempt = false, want true")
		}

		// A stale write for an earlier attempt (e.g. delayed by a retry, racing
		// behind a newer attempt's result) must not clobber the newer row.
		stale := &run.Step{ProjectID: projectID, RunID: runID, StepName: "cas", Status: "failed", Attempts: 1}
		applied, err = repo.UpsertCAS(ctx, stale)
		if err != nil {
			t.Fatalf("UpsertCAS stale attempt: %v", err)
		}
		if applied {
			t.Fatal("UpsertCAS stale attempt = true, want false")
		}
		steps, err := repo.List(ctx, projectID, runID)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(steps) != 1 {
			t.Fatalf("expected 1 step, got %d", len(steps))
		}
		if steps[0].Status != "done" || steps[0].Attempts != 2 {
			t.Errorf("stale UpsertCAS clobbered the newer row: got status=%q attempts=%d, want status=done attempts=2", steps[0].Status, steps[0].Attempts)
		}
	})

	t.Run("UpsertCAS_same_attempt_terminal_status_cannot_regress", func(t *testing.T) {
		// Reproduces a worker restart/outbox-retransmission race: a step
		// finishes (attempt=1, done), then a delayed re-send of its own
		// earlier "running" report for the *same* attempt arrives after.
		// attempts >= alone would let this through (1 >= 1) and silently
		// move the row back from done to running — UpsertCAS must reject it
		// instead, the same way it already rejects a strictly-lower attempt.
		runID := uuid.NewString()
		done := &run.Step{ProjectID: projectID, RunID: runID, StepName: "cas-regress", Status: "done", Attempts: 1}
		applied, err := repo.UpsertCAS(ctx, done)
		if err != nil {
			t.Fatalf("UpsertCAS done: %v", err)
		}
		if !applied {
			t.Fatal("UpsertCAS done = false, want true")
		}

		lateRunning := &run.Step{ProjectID: projectID, RunID: runID, StepName: "cas-regress", Status: "running", Attempts: 1}
		applied, err = repo.UpsertCAS(ctx, lateRunning)
		if err != nil {
			t.Fatalf("UpsertCAS late same-attempt running: %v", err)
		}
		if applied {
			t.Fatal("UpsertCAS late same-attempt running = true, want false (must not regress a terminal row)")
		}
		steps, err := repo.List(ctx, projectID, runID)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(steps) != 1 || steps[0].Status != "done" {
			t.Errorf("terminal row regressed: got %#v, want status=done", steps)
		}
	})

	t.Run("ListByRuns", func(t *testing.T) {
		runA := uuid.NewString()
		runB := uuid.NewString()
		for _, step := range []*run.Step{
			{ProjectID: projectID, RunID: runA, StepName: "a1", Status: "pending"},
			{ProjectID: projectID, RunID: runA, StepName: "a2", Status: "done"},
			{ProjectID: projectID, RunID: runB, StepName: "b1", Status: "running"},
		} {
			if err := repo.Upsert(ctx, step); err != nil {
				t.Fatalf("Upsert %q: %v", step.StepName, err)
			}
		}
		grouped, err := repo.ListByRuns(ctx, projectID, []string{runA, runB})
		if err != nil {
			t.Fatalf("ListByRuns: %v", err)
		}
		if len(grouped[runA]) != 2 {
			t.Fatalf("runA steps = %d, want 2", len(grouped[runA]))
		}
		if len(grouped[runB]) != 1 {
			t.Fatalf("runB steps = %d, want 1", len(grouped[runB]))
		}
	})

	t.Run("ListNonTerminalByWorker", func(t *testing.T) {
		workerID := uuid.NewString()
		otherWorkerID := uuid.NewString()
		runID := uuid.NewString()
		for _, step := range []*run.Step{
			{ProjectID: projectID, RunID: runID, StepName: "running", Status: run.StepStatusRunning, WorkerID: workerID},
			{ProjectID: projectID, RunID: runID, StepName: "done", Status: run.StepStatusDone, WorkerID: workerID},
			{ProjectID: projectID, RunID: runID, StepName: "failed", Status: run.StepStatusFailed, WorkerID: workerID},
			{ProjectID: projectID, RunID: runID, StepName: "skipped", Status: run.StepStatusSkipped, WorkerID: workerID},
			{ProjectID: projectID, RunID: runID, StepName: "canceled", Status: run.StepStatusCanceled, WorkerID: workerID},
			{ProjectID: projectID, RunID: runID, StepName: "other-worker-running", Status: run.StepStatusRunning, WorkerID: otherWorkerID},
		} {
			if err := repo.Upsert(ctx, step); err != nil {
				t.Fatalf("Upsert %q: %v", step.StepName, err)
			}
		}
		steps, err := repo.ListNonTerminalByWorker(ctx, workerID)
		if err != nil {
			t.Fatalf("ListNonTerminalByWorker: %v", err)
		}
		if len(steps) != 1 {
			t.Fatalf("expected 1 non-terminal step for workerID, got %d: %#v", len(steps), steps)
		}
		if steps[0].StepName != "running" {
			t.Errorf("StepName = %q, want %q", steps[0].StepName, "running")
		}
		if steps[0].WorkerID != workerID {
			t.Errorf("WorkerID = %q, want %q", steps[0].WorkerID, workerID)
		}
	})

	t.Run("DeleteByRun", func(t *testing.T) {
		runID := uuid.NewString()
		for _, name := range []string{"step-a", "step-b"} {
			if err := repo.Upsert(ctx, &run.Step{ProjectID: projectID, RunID: runID, StepName: name, Status: "pending"}); err != nil {
				t.Fatalf("Upsert %q: %v", name, err)
			}
		}
		if err := repo.DeleteByRun(ctx, projectID, runID); err != nil {
			t.Fatalf("DeleteByRun: %v", err)
		}
		steps, err := repo.List(ctx, projectID, runID)
		if err != nil {
			t.Fatalf("List after DeleteByRun: %v", err)
		}
		if len(steps) != 0 {
			t.Errorf("expected 0 steps after DeleteByRun, got %d", len(steps))
		}
	})
}
