// Package repotest provides shared contract tests for Repository implementations.
// Use RunRepoSuite and StepRepoSuite from both SQLite and PostgreSQL test packages
// to ensure both drivers satisfy the same behavioral contract.
package repotest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/piper/piper/pkg/pipeline/run"
)

// RunRepoSuite runs a full CRUD contract test against any run.Repository implementation.
func RunRepoSuite(t *testing.T, repo run.Repository) {
	t.Helper()
	ctx := context.Background()

	t.Run("Create_and_Get", func(t *testing.T) {
		r := &run.Run{
			ID:           uuid.NewString(),
			PipelineName: "test-pipeline",
			Status:       run.StatusRunning,
			StartedAt:    time.Now().UTC().Truncate(time.Millisecond),
		}
		if err := repo.Create(ctx, r); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := repo.Get(ctx, r.ID)
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
		got, _ := repo.Get(ctx, "nonexistent-id")
		if got != nil {
			t.Errorf("expected nil record for missing run, got %+v", got)
		}
	})

	t.Run("List_by_status", func(t *testing.T) {
		id := uuid.NewString()
		if err := repo.Create(ctx, &run.Run{
			ID:           id,
			PipelineName: "list-test",
			Status:       run.StatusFailed,
			StartedAt:    time.Now().UTC(),
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		runs, err := repo.List(ctx, run.RunFilter{Status: run.StatusFailed})
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
			PipelineName: "update-test",
			Status:       run.StatusRunning,
			StartedAt:    time.Now().UTC(),
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		if err := repo.UpdateStatus(ctx, id, run.StatusSuccess, &now); err != nil {
			t.Fatalf("UpdateStatus: %v", err)
		}
		got, err := repo.Get(ctx, id)
		if err != nil || got == nil {
			t.Fatalf("Get after UpdateStatus: %v, got=%v", err, got)
		}
		if got.Status != run.StatusSuccess {
			t.Errorf("status mismatch: got %q want %q", got.Status, run.StatusSuccess)
		}
	})

	t.Run("GetLatestSuccessful", func(t *testing.T) {
		pname := "pipeline-" + uuid.NewString()
		now := time.Now().UTC()
		for i, status := range []string{run.StatusFailed, run.StatusSuccess, run.StatusSuccess} {
			endedAt := now.Add(time.Duration(i) * time.Second)
			if err := repo.Create(ctx, &run.Run{
				ID:           uuid.NewString(),
				PipelineName: pname,
				Status:       status,
				StartedAt:    now.Add(time.Duration(i) * time.Second),
				EndedAt:      &endedAt,
			}); err != nil {
				t.Fatalf("Create: %v", err)
			}
		}
		got, err := repo.GetLatestSuccessful(ctx, pname)
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
		missing, err := repo.GetLatestSuccessful(ctx, "no-such-pipeline")
		if err != nil {
			t.Fatalf("GetLatestSuccessful(missing): %v", err)
		}
		if missing != nil {
			t.Errorf("expected nil for missing pipeline, got %+v", missing)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		id := uuid.NewString()
		if err := repo.Create(ctx, &run.Run{
			ID:           id,
			PipelineName: "delete-test",
			Status:       run.StatusSuccess,
			StartedAt:    time.Now().UTC(),
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := repo.Delete(ctx, id); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		// After deletion, Get should return nil record (error or nil).
		got, _ := repo.Get(ctx, id)
		if got != nil {
			t.Errorf("expected nil record after delete, got %+v", got)
		}
	})
}

// StepRepoSuite runs a contract test against any run.StepRepository implementation.
func StepRepoSuite(t *testing.T, repo run.StepRepository) {
	t.Helper()
	ctx := context.Background()

	t.Run("Upsert_and_List", func(t *testing.T) {
		runID := uuid.NewString()
		step := &run.Step{
			RunID:    runID,
			StepName: "train",
			Status:   "pending",
		}
		if err := repo.Upsert(ctx, step); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		steps, err := repo.List(ctx, runID)
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

	t.Run("Upsert_updates_existing", func(t *testing.T) {
		runID := uuid.NewString()
		step := &run.Step{RunID: runID, StepName: "eval", Status: "pending"}
		if err := repo.Upsert(ctx, step); err != nil {
			t.Fatalf("Upsert initial: %v", err)
		}
		step.Status = "done"
		if err := repo.Upsert(ctx, step); err != nil {
			t.Fatalf("Upsert update: %v", err)
		}
		steps, err := repo.List(ctx, runID)
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

	t.Run("DeleteByRun", func(t *testing.T) {
		runID := uuid.NewString()
		for _, name := range []string{"step-a", "step-b"} {
			if err := repo.Upsert(ctx, &run.Step{RunID: runID, StepName: name, Status: "pending"}); err != nil {
				t.Fatalf("Upsert %q: %v", name, err)
			}
		}
		if err := repo.DeleteByRun(ctx, runID); err != nil {
			t.Fatalf("DeleteByRun: %v", err)
		}
		steps, err := repo.List(ctx, runID)
		if err != nil {
			t.Fatalf("List after DeleteByRun: %v", err)
		}
		if len(steps) != 0 {
			t.Errorf("expected 0 steps after DeleteByRun, got %d", len(steps))
		}
	})
}
