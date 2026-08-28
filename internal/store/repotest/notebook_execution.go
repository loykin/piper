package repotest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/loykin/piper/pkg/notebook/execution"
)

// NotebookExecutionRepoSuite exercises execution.Repository: KernelSession
// CRUD/admission counting, NotebookExecution CRUD/idempotency/status-scan,
// and the project-level notebook_execution_policy override (design doc
// docs/jupyter-mcp-execution.md §5, §7, §9.3, §11.1, §11.2).
func NotebookExecutionRepoSuite(t *testing.T, repo execution.Repository, projectID string) {
	t.Helper()
	ctx := context.Background()

	newKernelSession := func(notebookName, createdBy string) *execution.KernelSession {
		now := time.Now().UTC().Truncate(time.Second)
		return &execution.KernelSession{
			ID:               uuid.NewString(),
			ProjectID:        projectID,
			NotebookName:     notebookName,
			NotebookPath:     "nb/" + notebookName + ".ipynb",
			JupyterSessionID: "jsess-" + uuid.NewString(),
			KernelID:         "kern-" + uuid.NewString(),
			KernelName:       "python3",
			Status:           execution.KernelStatusIdle,
			CreatedBy:        createdBy,
			ClientID:         "rest",
			LastActivityAt:   now,
			CreatedAt:        now,
		}
	}

	t.Run("KernelSession_create_get_update", func(t *testing.T) {
		k := newKernelSession("alpha", "alice")
		if err := repo.CreateKernelSession(ctx, k); err != nil {
			t.Fatalf("CreateKernelSession: %v", err)
		}

		got, err := repo.GetKernelSession(ctx, projectID, k.ID)
		if err != nil {
			t.Fatalf("GetKernelSession: %v", err)
		}
		if got == nil || got.NotebookName != "alpha" || got.KernelID != k.KernelID {
			t.Fatalf("GetKernelSession = %#v, want a match for %#v", got, k)
		}

		missing, err := repo.GetKernelSession(ctx, projectID, "does-not-exist")
		if err != nil || missing != nil {
			t.Fatalf("GetKernelSession(missing) = %#v, err=%v, want nil, nil", missing, err)
		}

		got.Status = execution.KernelStatusBusy
		got.LastActivityAt = time.Now().UTC().Truncate(time.Second)
		if err := repo.UpdateKernelSession(ctx, got); err != nil {
			t.Fatalf("UpdateKernelSession: %v", err)
		}
		reGot, err := repo.GetKernelSession(ctx, projectID, k.ID)
		if err != nil || reGot.Status != execution.KernelStatusBusy {
			t.Fatalf("GetKernelSession after update = %#v, err=%v, want status=busy", reGot, err)
		}

		notFound := &execution.KernelSession{ID: "nope", ProjectID: projectID}
		if err := repo.UpdateKernelSession(ctx, notFound); !errors.Is(err, execution.ErrNotFound) {
			t.Fatalf("UpdateKernelSession(missing): err = %v, want ErrNotFound", err)
		}
	})

	t.Run("KernelSession_list_scoped_by_owner_and_count_open", func(t *testing.T) {
		notebookName := "beta-" + uuid.NewString()[:8]
		a := newKernelSession(notebookName, "alice")
		b := newKernelSession(notebookName, "bob")
		if err := repo.CreateKernelSession(ctx, a); err != nil {
			t.Fatalf("CreateKernelSession a: %v", err)
		}
		if err := repo.CreateKernelSession(ctx, b); err != nil {
			t.Fatalf("CreateKernelSession b: %v", err)
		}

		aliceOnly, err := repo.ListKernelSessions(ctx, projectID, notebookName, "alice", 0, 0)
		if err != nil {
			t.Fatalf("ListKernelSessions(alice): %v", err)
		}
		if len(aliceOnly) != 1 || aliceOnly[0].ID != a.ID {
			t.Fatalf("ListKernelSessions(alice) = %#v, want just a", aliceOnly)
		}

		all, err := repo.ListKernelSessions(ctx, projectID, notebookName, "", 0, 0)
		if err != nil {
			t.Fatalf("ListKernelSessions(all): %v", err)
		}
		if len(all) != 2 {
			t.Fatalf("ListKernelSessions(all) = %d sessions, want 2", len(all))
		}

		openCount, err := repo.CountOpenKernelSessions(ctx, projectID, notebookName)
		if err != nil {
			t.Fatalf("CountOpenKernelSessions: %v", err)
		}
		if openCount != 2 {
			t.Fatalf("CountOpenKernelSessions = %d, want 2", openCount)
		}

		now := time.Now().UTC()
		b.Status = execution.KernelStatusClosed
		b.ClosedAt = &now
		if err := repo.UpdateKernelSession(ctx, b); err != nil {
			t.Fatalf("UpdateKernelSession close b: %v", err)
		}
		openCount, err = repo.CountOpenKernelSessions(ctx, projectID, notebookName)
		if err != nil {
			t.Fatalf("CountOpenKernelSessions after close: %v", err)
		}
		if openCount != 1 {
			t.Fatalf("CountOpenKernelSessions after close = %d, want 1", openCount)
		}
	})

	t.Run("KernelSession_stale_ttl_sweep_candidates", func(t *testing.T) {
		notebookName := "ttl-" + uuid.NewString()[:8]
		stale := newKernelSession(notebookName, "carol")
		stale.LastActivityAt = time.Now().UTC().Add(-time.Hour)
		if err := repo.CreateKernelSession(ctx, stale); err != nil {
			t.Fatalf("CreateKernelSession stale: %v", err)
		}
		fresh := newKernelSession(notebookName, "carol")
		fresh.LastActivityAt = time.Now().UTC()
		if err := repo.CreateKernelSession(ctx, fresh); err != nil {
			t.Fatalf("CreateKernelSession fresh: %v", err)
		}

		cutoff := time.Now().UTC().Add(-30 * time.Minute)
		candidates, err := repo.ListStaleKernelSessions(ctx, cutoff)
		if err != nil {
			t.Fatalf("ListStaleKernelSessions: %v", err)
		}
		foundStale, foundFresh := false, false
		for _, k := range candidates {
			if k.ID == stale.ID {
				foundStale = true
			}
			if k.ID == fresh.ID {
				foundFresh = true
			}
		}
		if !foundStale {
			t.Fatalf("ListStaleKernelSessions did not include the stale session")
		}
		if foundFresh {
			t.Fatalf("ListStaleKernelSessions incorrectly included the fresh session")
		}
	})

	newExecution := func(notebookName, requestedBy, idemKey string) *execution.NotebookExecution {
		now := time.Now().UTC().Truncate(time.Second)
		id := uuid.NewString()
		return &execution.NotebookExecution{
			ID:              id,
			ProjectID:       projectID,
			NotebookName:    notebookName,
			NotebookPath:    "nb/" + notebookName + ".ipynb",
			ResultPath:      ".piper/executions/" + id + "/result.ipynb",
			Kind:            execution.KindNotebook,
			Status:          execution.StatusQueued,
			RequestedBy:     requestedBy,
			ClientID:        "rest",
			IdempotencyKey:  idemKey,
			RequestHash:     "hash-" + idemKey,
			BaseContentHash: "base-hash",
			QueuedAt:        now,
			UpdatedAt:       now,
		}
	}

	t.Run("Execution_create_get_list_count", func(t *testing.T) {
		notebookName := "exec-" + uuid.NewString()[:8]
		e1 := newExecution(notebookName, "alice", "")
		e2 := newExecution(notebookName, "bob", "")
		if err := repo.CreateExecution(ctx, e1); err != nil {
			t.Fatalf("CreateExecution e1: %v", err)
		}
		if err := repo.CreateExecution(ctx, e2); err != nil {
			t.Fatalf("CreateExecution e2: %v", err)
		}

		got, err := repo.GetExecution(ctx, projectID, e1.ID)
		if err != nil || got == nil || got.RequestedBy != "alice" {
			t.Fatalf("GetExecution = %#v, err=%v, want alice's execution", got, err)
		}

		missing, err := repo.GetExecution(ctx, projectID, "does-not-exist")
		if err != nil || missing != nil {
			t.Fatalf("GetExecution(missing) = %#v, err=%v, want nil, nil", missing, err)
		}

		list, err := repo.ListExecutions(ctx, projectID, notebookName, 0, 0)
		if err != nil {
			t.Fatalf("ListExecutions: %v", err)
		}
		if len(list) != 2 {
			t.Fatalf("ListExecutions returned %d, want 2", len(list))
		}
		count, err := repo.CountExecutions(ctx, projectID, notebookName)
		if err != nil {
			t.Fatalf("CountExecutions: %v", err)
		}
		if count != 2 {
			t.Fatalf("CountExecutions = %d, want 2", count)
		}
	})

	t.Run("Execution_update_status_transition_and_not_found", func(t *testing.T) {
		notebookName := "update-" + uuid.NewString()[:8]
		e := newExecution(notebookName, "alice", "")
		if err := repo.CreateExecution(ctx, e); err != nil {
			t.Fatalf("CreateExecution: %v", err)
		}
		now := time.Now().UTC().Truncate(time.Second)
		e.Status = execution.StatusRunning
		e.StartedAt = &now
		e.CurrentCell = 1
		e.TotalCells = 3
		if err := repo.UpdateExecution(ctx, e); err != nil {
			t.Fatalf("UpdateExecution: %v", err)
		}
		got, err := repo.GetExecution(ctx, projectID, e.ID)
		if err != nil {
			t.Fatalf("GetExecution after update: %v", err)
		}
		if got.Status != execution.StatusRunning || got.CurrentCell != 1 || got.TotalCells != 3 {
			t.Fatalf("GetExecution after update = %#v, want running/1/3", got)
		}
		if got.StartedAt == nil || !got.StartedAt.Equal(now) {
			t.Fatalf("StartedAt = %v, want %v", got.StartedAt, now)
		}

		notFound := &execution.NotebookExecution{ID: "nope", ProjectID: projectID}
		if err := repo.UpdateExecution(ctx, notFound); !errors.Is(err, execution.ErrNotFound) {
			t.Fatalf("UpdateExecution(missing): err = %v, want ErrNotFound", err)
		}
	})

	t.Run("Execution_running_and_queued_counts", func(t *testing.T) {
		notebookName := "counts-" + uuid.NewString()[:8]
		running := newExecution(notebookName, "alice", "")
		running.Status = execution.StatusRunning
		queued := newExecution(notebookName, "alice", "")
		queued.Status = execution.StatusQueued
		awaiting := newExecution(notebookName, "alice", "")
		awaiting.Status = execution.StatusAwaitingApproval
		done := newExecution(notebookName, "alice", "")
		done.Status = execution.StatusSucceeded
		for _, e := range []*execution.NotebookExecution{running, queued, awaiting, done} {
			if err := repo.CreateExecution(ctx, e); err != nil {
				t.Fatalf("CreateExecution %s: %v", e.Status, err)
			}
		}

		runningCount, err := repo.CountRunningExecutions(ctx, projectID, notebookName)
		if err != nil {
			t.Fatalf("CountRunningExecutions: %v", err)
		}
		if runningCount != 1 {
			t.Fatalf("CountRunningExecutions = %d, want 1", runningCount)
		}

		queuedCount, err := repo.CountQueuedExecutions(ctx, projectID)
		if err != nil {
			t.Fatalf("CountQueuedExecutions: %v", err)
		}
		if queuedCount < 2 {
			t.Fatalf("CountQueuedExecutions = %d, want at least 2 (queued + awaiting_approval)", queuedCount)
		}
	})

	t.Run("Execution_idempotency_replay_and_conflict", func(t *testing.T) {
		notebookName := "idem-" + uuid.NewString()[:8]
		key := "idem-key-" + uuid.NewString()[:8]
		e := newExecution(notebookName, "alice", key)
		if err := repo.CreateExecution(ctx, e); err != nil {
			t.Fatalf("CreateExecution: %v", err)
		}

		found, err := repo.FindExecutionByIdempotencyKey(ctx, projectID, notebookName, "alice", key)
		if err != nil {
			t.Fatalf("FindExecutionByIdempotencyKey: %v", err)
		}
		if found == nil || found.ID != e.ID {
			t.Fatalf("FindExecutionByIdempotencyKey = %#v, want execution %s", found, e.ID)
		}

		// A different actor with the same key against the same notebook is a
		// distinct logical request — the (project, notebook, actor, key)
		// tuple is the uniqueness/lookup scope (design doc §7.3).
		notFoundForBob, err := repo.FindExecutionByIdempotencyKey(ctx, projectID, notebookName, "bob", key)
		if err != nil {
			t.Fatalf("FindExecutionByIdempotencyKey(bob): %v", err)
		}
		if notFoundForBob != nil {
			t.Fatalf("FindExecutionByIdempotencyKey(bob) = %#v, want nil (different actor)", notFoundForBob)
		}

		// Inserting a second row with the exact same
		// (project, notebook, actor, key) tuple must be rejected by the
		// unique index — the service layer normally prevents this by
		// looking up first, but the DB constraint is the real guarantee
		// under concurrent requests.
		dup := newExecution(notebookName, "alice", key)
		if err := repo.CreateExecution(ctx, dup); !errors.Is(err, execution.ErrConflict) {
			t.Fatalf("CreateExecution duplicate idempotency key: err = %v, want ErrConflict", err)
		}
	})

	t.Run("Execution_list_by_status_for_recovery_scan", func(t *testing.T) {
		notebookName := "recover-" + uuid.NewString()[:8]
		queued := newExecution(notebookName, "alice", "")
		queued.Status = execution.StatusQueued
		running := newExecution(notebookName, "alice", "")
		running.Status = execution.StatusRunning
		cancelling := newExecution(notebookName, "alice", "")
		cancelling.Status = execution.StatusCancelling
		terminal := newExecution(notebookName, "alice", "")
		terminal.Status = execution.StatusFailed
		for _, e := range []*execution.NotebookExecution{queued, running, cancelling, terminal} {
			if err := repo.CreateExecution(ctx, e); err != nil {
				t.Fatalf("CreateExecution %s: %v", e.Status, err)
			}
		}

		active, err := repo.ListExecutionsByStatus(ctx, execution.ActiveExecutionStatuses)
		if err != nil {
			t.Fatalf("ListExecutionsByStatus: %v", err)
		}
		ids := map[string]bool{}
		for _, e := range active {
			ids[e.ID] = true
		}
		for _, want := range []string{queued.ID, running.ID, cancelling.ID} {
			if !ids[want] {
				t.Fatalf("ListExecutionsByStatus missing expected active execution %s; got %d rows", want, len(active))
			}
		}
		if ids[terminal.ID] {
			t.Fatalf("ListExecutionsByStatus incorrectly included a terminal execution")
		}
	})

	t.Run("ExecutionPolicy_default_empty_then_set_and_overwrite", func(t *testing.T) {
		empty, err := repo.GetExecutionPolicy(ctx, projectID)
		if err != nil {
			t.Fatalf("GetExecutionPolicy(no override): %v", err)
		}
		if empty != "" {
			t.Fatalf("GetExecutionPolicy(no override) = %q, want empty (caller falls back to system default)", empty)
		}

		if err := repo.SetExecutionPolicy(ctx, projectID, execution.PolicyAllowed, "admin-1"); err != nil {
			t.Fatalf("SetExecutionPolicy: %v", err)
		}
		got, err := repo.GetExecutionPolicy(ctx, projectID)
		if err != nil || got != execution.PolicyAllowed {
			t.Fatalf("GetExecutionPolicy after set = %q, err=%v, want %q", got, err, execution.PolicyAllowed)
		}

		if err := repo.SetExecutionPolicy(ctx, projectID, execution.PolicyDisabled, "admin-2"); err != nil {
			t.Fatalf("SetExecutionPolicy overwrite: %v", err)
		}
		got, err = repo.GetExecutionPolicy(ctx, projectID)
		if err != nil || got != execution.PolicyDisabled {
			t.Fatalf("GetExecutionPolicy after overwrite = %q, err=%v, want %q", got, err, execution.PolicyDisabled)
		}
	})
}
