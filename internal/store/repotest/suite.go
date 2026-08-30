// Package repotest provides shared contract tests for Repository implementations.
// Use RunRepoSuite and StepRepoSuite from both SQLite and PostgreSQL test packages
// to ensure both drivers satisfy the same behavioral contract.
package repotest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/loykin/piper/internal/projectclient"
	"github.com/loykin/piper/pkg/credential"
	"github.com/loykin/piper/pkg/federation"
	"github.com/loykin/piper/pkg/integration/mlflow"
	"github.com/loykin/piper/pkg/integration/outbox"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/template"
)

func FederationRepoSuite(t *testing.T, repo federation.Repository) {
	t.Helper()
	ctx := context.Background()
	configuredAt := time.Now().UTC().Truncate(time.Millisecond)
	if err := repo.SyncConfiguredMembers(ctx, "home-a", []string{"member-a", "member-b"}, configuredAt); err != nil {
		t.Fatalf("SyncConfiguredMembers: %v", err)
	}
	members, err := repo.ListMembers(ctx, "home-a")
	if err != nil || len(members) != 2 {
		t.Fatalf("ListMembers = %#v, %v", members, err)
	}
	connectedAt := configuredAt.Add(time.Second)
	if err := repo.SetMemberConnected(ctx, "home-a", "member-a", true, connectedAt); err != nil {
		t.Fatalf("connect member: %v", err)
	}
	if err := repo.SetMemberConnected(ctx, "home-a", "member-a", false, connectedAt.Add(time.Second)); err != nil {
		t.Fatalf("disconnect member: %v", err)
	}
	events, err := repo.ListAuditEvents(ctx, "home-a", 10)
	if err != nil || len(events) != 2 || events[0].Type != federation.AuditMemberDisconnected || events[1].Type != federation.AuditMemberConnected {
		t.Fatalf("audit events = %#v, %v", events, err)
	}
	if err := repo.SetMemberConnected(ctx, "home-a", "unknown", true, connectedAt); !errors.Is(err, federation.ErrMemberNotConfigured) {
		t.Fatalf("unknown member error = %v", err)
	}
	projectValue := &project.Project{ID: "federation-" + uuid.NewString(), Name: "federated", OwnerMemberID: "member-a"}
	if err := repo.CreateProject(ctx, "home-a", projectValue, "admin-a"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := repo.SetProjectOwner(ctx, "home-a", projectValue.ID, "member-b", "admin-b", connectedAt.Add(3*time.Second)); err != nil {
		t.Fatalf("SetProjectOwner: %v", err)
	}
	events, err = repo.ListAuditEvents(ctx, "home-a", 10)
	createdFound := false
	for _, event := range events {
		createdFound = createdFound || event.Type == federation.AuditProjectCreated
	}
	if err != nil || len(events) != 4 || events[0].Type != federation.AuditProjectOwnerSet || events[0].Detail != "member-a" || !createdFound {
		t.Fatalf("project audit events = %#v, %v", events, err)
	}
	if err := repo.SyncConfiguredMembers(ctx, "home-a", []string{"member-a"}, connectedAt.Add(4*time.Second)); err != nil {
		t.Fatalf("resync members: %v", err)
	}
	members, err = repo.ListMembers(ctx, "home-a")
	if err != nil || len(members) != 2 || !members[0].Enabled || members[1].Enabled {
		t.Fatalf("members after resync = %#v, %v", members, err)
	}
}

func SubmissionRepoSuite(t *testing.T, repo run.SubmissionRepository, projectID string) {
	t.Helper()
	ctx := context.Background()
	first := &run.Submission{
		ProjectID: projectID, Key: "request-a", RequestHash: "hash-a",
		RunID: "run-a", CreatedAt: time.Now().UTC(),
	}
	existing, claimed, err := repo.Claim(ctx, first)
	if err != nil || !claimed || existing.RunID != "run-a" {
		t.Fatalf("first Claim = %#v, %v, %v", existing, claimed, err)
	}
	existing, claimed, err = repo.Claim(ctx, &run.Submission{
		ProjectID: projectID, Key: "request-a", RequestHash: "different",
		RunID: "run-b", CreatedAt: time.Now().UTC(),
	})
	if err != nil || claimed || existing.RunID != "run-a" || existing.RequestHash != "hash-a" {
		t.Fatalf("duplicate Claim = %#v, %v, %v", existing, claimed, err)
	}
	if err := repo.Delete(ctx, projectID, "request-a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, claimed, err = repo.Claim(ctx, first)
	if err != nil || !claimed {
		t.Fatalf("Claim after Delete = %v, %v", claimed, err)
	}
}

func ProjectMutationRepoSuite(t *testing.T, repo projectclient.MutationRepository, projectID string) {
	t.Helper()
	ctx := context.Background()
	first := &projectclient.Mutation{ProjectID: projectID, Key: "mutation-a", RequestHash: "hash-a", CreatedAt: time.Now().UTC()}
	existing, claimed, err := repo.Claim(ctx, first)
	if err != nil || !claimed || existing.Completed {
		t.Fatalf("first Claim=%#v %v %v", existing, claimed, err)
	}
	existing, claimed, err = repo.Claim(ctx, &projectclient.Mutation{ProjectID: projectID, Key: "mutation-a", RequestHash: "other", CreatedAt: time.Now().UTC()})
	if err != nil || claimed || existing.RequestHash != "hash-a" {
		t.Fatalf("duplicate Claim=%#v %v %v", existing, claimed, err)
	}
	first.Status = 201
	first.HeaderJSON = []byte(`{"X-Test":["yes"]}`)
	first.Body = []byte("created")
	first.Completed = true
	if err := repo.Complete(ctx, first); err != nil {
		t.Fatal(err)
	}
	existing, claimed, err = repo.Claim(ctx, first)
	if err != nil || claimed || !existing.Completed || existing.Status != 201 || string(existing.Body) != "created" {
		t.Fatalf("completed Claim=%#v %v %v", existing, claimed, err)
	}

	// A claim that never completed (e.g. the process crashed, or Complete's
	// persistence write itself failed) must not stay stuck forever - once
	// it's older than projectclient.StaleClaimWindow, a retry carrying the
	// same key is allowed to reclaim and re-run it. Time is simulated via
	// the CreatedAt each attempt supplies (matching how the real caller
	// stamps CreatedAt with time.Now() on every attempt), not by sleeping.
	t0 := time.Now().UTC()
	stuck := &projectclient.Mutation{ProjectID: projectID, Key: "mutation-b", RequestHash: "hash-b", CreatedAt: t0}
	if _, claimed, err := repo.Claim(ctx, stuck); err != nil || !claimed {
		t.Fatalf("initial stuck Claim=%v %v", claimed, err)
	}
	tooSoon := &projectclient.Mutation{ProjectID: projectID, Key: "mutation-b", RequestHash: "hash-b", CreatedAt: t0.Add(time.Second)}
	if existing, claimed, err := repo.Claim(ctx, tooSoon); err != nil || claimed || existing.Completed {
		t.Fatalf("reclaim before stale window elapsed should not happen: claimed=%v existing=%#v err=%v", claimed, existing, err)
	}
	reclaim := &projectclient.Mutation{ProjectID: projectID, Key: "mutation-b", RequestHash: "hash-b", CreatedAt: t0.Add(projectclient.StaleClaimWindow + time.Second)}
	existing, claimed, err = repo.Claim(ctx, reclaim)
	if err != nil || !claimed || existing.Completed {
		t.Fatalf("reclaim of stale incomplete claim=%#v %v %v", existing, claimed, err)
	}
}

func ProjectRepoSuite(t *testing.T, repo project.Repository) {
	t.Helper()
	ctx := context.Background()

	first := &project.Project{ID: uuid.NewString(), Name: "alpha", Description: "first", OwnerMemberID: "member-a"}
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
	if got == nil || got.ID != first.ID || got.Name != first.Name || got.OwnerMemberID != "member-a" {
		t.Fatalf("Get project = %#v, want %#v", got, first)
	}
	if err := repo.SetOwner(ctx, first.ID, "member-b"); err != nil {
		t.Fatalf("SetOwner: %v", err)
	}
	got, err = repo.Get(ctx, first.ID)
	if err != nil || got == nil || got.OwnerMemberID != "member-b" {
		t.Fatalf("owner after SetOwner = %#v, %v", got, err)
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

	t.Run("StorageBackend_roundtrips_through_Create_Get_and_List", func(t *testing.T) {
		r := &run.Run{
			ID:             uuid.NewString(),
			ProjectID:      projectID,
			PipelineName:   "storage-backend-test",
			Status:         run.StatusRunning,
			StartedAt:      time.Now().UTC().Truncate(time.Millisecond),
			StorageBackend: "s3:my-bucket",
		}
		if err := repo.Create(ctx, r); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := repo.Get(ctx, projectID, r.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.StorageBackend != "s3:my-bucket" {
			t.Errorf("Get: StorageBackend = %q, want %q", got.StorageBackend, "s3:my-bucket")
		}

		rows, err := repo.List(ctx, projectID, run.RunFilter{PipelineName: "storage-backend-test"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		found := false
		for _, row := range rows {
			if row.ID == r.ID {
				found = true
				if row.StorageBackend != "s3:my-bucket" {
					t.Errorf("List: StorageBackend = %q, want %q", row.StorageBackend, "s3:my-bucket")
				}
			}
		}
		if !found {
			t.Fatalf("List did not return created run %q", r.ID)
		}

		// A run created without an explicit StorageBackend (the common case
		// for rows written before this field existed) must round-trip as ""
		// — never silently defaulted to something else — since the read-time
		// mismatch check treats "" as "unknown, don't flag" rather than "no
		// backend".
		unstamped := &run.Run{
			ID:           uuid.NewString(),
			ProjectID:    projectID,
			PipelineName: "storage-backend-unstamped-test",
			Status:       run.StatusRunning,
			StartedAt:    time.Now().UTC().Truncate(time.Millisecond),
		}
		if err := repo.Create(ctx, unstamped); err != nil {
			t.Fatalf("Create (unstamped): %v", err)
		}
		gotUnstamped, err := repo.Get(ctx, projectID, unstamped.ID)
		if err != nil {
			t.Fatalf("Get (unstamped): %v", err)
		}
		if gotUnstamped.StorageBackend != "" {
			t.Errorf("Get (unstamped): StorageBackend = %q, want empty", gotUnstamped.StorageBackend)
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

// MLflowRepoSuite exercises mlflow.Repository: MLflowIntegration CRUD
// (including the "at most one Default=true integration per project"
// invariant, design doc section 5.1) plus the experiment/run link mapping
// tables (design doc section 6).
func MLflowRepoSuite(t *testing.T, repo mlflow.Repository, projectID string) {
	t.Helper()
	ctx := context.Background()

	newIntegration := func(name string, isDefault bool) *mlflow.MLflowIntegration {
		return &mlflow.MLflowIntegration{
			ID:                 uuid.NewString(),
			ProjectID:          projectID,
			Name:               name,
			TrackingURI:        "https://" + name + ".mlflow.example.com",
			CredentialRef:      "mlflow-cred",
			Enabled:            true,
			Default:            isDefault,
			ExportPipelines:    true,
			ExperimentTemplate: mlflow.DefaultExperimentTemplate,
			ArtifactMode:       string(mlflow.ArtifactModeReference),
		}
	}

	t.Run("Create_get_and_list_integrations", func(t *testing.T) {
		a := newIntegration("alpha", false)
		if err := repo.CreateIntegration(ctx, a); err != nil {
			t.Fatalf("CreateIntegration: %v", err)
		}
		if a.CreatedAt.IsZero() || a.UpdatedAt.IsZero() {
			t.Fatalf("CreateIntegration did not stamp timestamps: %+v", a)
		}

		got, err := repo.GetIntegration(ctx, projectID, a.ID)
		if err != nil {
			t.Fatalf("GetIntegration: %v", err)
		}
		if got == nil || got.Name != "alpha" || got.TrackingURI != a.TrackingURI {
			t.Fatalf("GetIntegration = %#v, want alpha", got)
		}

		byName, err := repo.GetIntegrationByName(ctx, projectID, "alpha")
		if err != nil || byName == nil || byName.ID != a.ID {
			t.Fatalf("GetIntegrationByName = %#v, err=%v", byName, err)
		}

		missing, err := repo.GetIntegration(ctx, projectID, "does-not-exist")
		if err != nil || missing != nil {
			t.Fatalf("GetIntegration(missing) = %#v, err=%v, want nil, nil", missing, err)
		}

		b := newIntegration("beta", false)
		if err := repo.CreateIntegration(ctx, b); err != nil {
			t.Fatalf("CreateIntegration beta: %v", err)
		}

		list, err := repo.ListIntegrations(ctx, projectID, 0, 0)
		if err != nil {
			t.Fatalf("ListIntegrations: %v", err)
		}
		if len(list) != 2 {
			t.Fatalf("ListIntegrations returned %d integrations, want 2", len(list))
		}

		count, err := repo.CountIntegrations(ctx, projectID)
		if err != nil {
			t.Fatalf("CountIntegrations: %v", err)
		}
		if count != 2 {
			t.Fatalf("CountIntegrations = %d, want 2", count)
		}
	})

	t.Run("Create_duplicate_name_rejected", func(t *testing.T) {
		first := newIntegration("dup", false)
		if err := repo.CreateIntegration(ctx, first); err != nil {
			t.Fatalf("CreateIntegration: %v", err)
		}
		second := newIntegration("dup", false)
		if err := repo.CreateIntegration(ctx, second); !errors.Is(err, mlflow.ErrAlreadyExists) {
			t.Fatalf("CreateIntegration duplicate: err = %v, want ErrAlreadyExists", err)
		}
	})

	t.Run("Create_rejects_invalid_tracking_uri", func(t *testing.T) {
		bad := newIntegration("insecure", false)
		bad.TrackingURI = "http://insecure.mlflow.example.com"
		if err := repo.CreateIntegration(ctx, bad); !errors.Is(err, mlflow.ErrInvalid) {
			t.Fatalf("CreateIntegration(http): err = %v, want ErrInvalid", err)
		}
	})

	t.Run("At_most_one_default_per_project", func(t *testing.T) {
		first := newIntegration("default-one", true)
		if err := repo.CreateIntegration(ctx, first); err != nil {
			t.Fatalf("CreateIntegration first default: %v", err)
		}
		second := newIntegration("default-two", true)
		if err := repo.CreateIntegration(ctx, second); err != nil {
			t.Fatalf("CreateIntegration second default: %v", err)
		}

		gotFirst, err := repo.GetIntegration(ctx, projectID, first.ID)
		if err != nil {
			t.Fatalf("GetIntegration first: %v", err)
		}
		if gotFirst.Default {
			t.Fatalf("first integration still default after second was created as default")
		}
		gotSecond, err := repo.GetIntegration(ctx, projectID, second.ID)
		if err != nil {
			t.Fatalf("GetIntegration second: %v", err)
		}
		if !gotSecond.Default {
			t.Fatalf("second integration is not the default")
		}

		def, err := repo.GetDefaultIntegration(ctx, projectID)
		if err != nil {
			t.Fatalf("GetDefaultIntegration: %v", err)
		}
		if def == nil || def.ID != second.ID {
			t.Fatalf("GetDefaultIntegration = %#v, want %s", def, second.ID)
		}

		// Flipping first back to default should reclaim it from second.
		gotFirst.Default = true
		if err := repo.UpdateIntegration(ctx, gotFirst); err != nil {
			t.Fatalf("UpdateIntegration reclaim default: %v", err)
		}
		def, err = repo.GetDefaultIntegration(ctx, projectID)
		if err != nil {
			t.Fatalf("GetDefaultIntegration after reclaim: %v", err)
		}
		if def == nil || def.ID != first.ID {
			t.Fatalf("GetDefaultIntegration after reclaim = %#v, want %s", def, first.ID)
		}
	})

	// Regression for the adversarial-review finding that
	// idx_mlflow_integrations_default was a plain (non-unique) index, so
	// "at most one Default=true integration per project" was only enforced
	// by CreateIntegration/UpdateIntegration's "clear, then set"
	// transactional logic — not the database itself — and two concurrent
	// transactions targeting two *different* rows could each successfully
	// commit their own row as Default=true. Fired concurrently against a
	// real connection pool, this exercises the actual race rather than just
	// the sequential app-level logic ("At_most_one_default_per_project"
	// above already covers the sequential case). The partial unique index
	// (project_id) WHERE is_default=TRUE AND deleted_at IS NULL now makes
	// this a DB-enforced invariant: one of the two concurrent updates must
	// fail rather than both silently succeeding.
	t.Run("Concurrent_default_updates_never_leave_two_defaults", func(t *testing.T) {
		a := newIntegration("concurrent-default-a", false)
		if err := repo.CreateIntegration(ctx, a); err != nil {
			t.Fatalf("CreateIntegration a: %v", err)
		}
		b := newIntegration("concurrent-default-b", false)
		if err := repo.CreateIntegration(ctx, b); err != nil {
			t.Fatalf("CreateIntegration b: %v", err)
		}

		a.Default = true
		b.Default = true
		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Add(2)
		go func() { defer wg.Done(); errs[0] = repo.UpdateIntegration(ctx, a) }()
		go func() { defer wg.Done(); errs[1] = repo.UpdateIntegration(ctx, b) }()
		wg.Wait()

		// Both calls are allowed to succeed (the second's transaction can
		// simply run after the first's commits, clearing it in the normal
		// sequential way) — what must never happen is both landing as
		// Default=true at once. Whichever ends up default, there must be
		// exactly one.
		list, err := repo.ListIntegrations(ctx, projectID, 0, 0)
		if err != nil {
			t.Fatalf("ListIntegrations: %v", err)
		}
		defaultCount := 0
		for _, row := range list {
			if row.ID == a.ID || row.ID == b.ID {
				if row.Default {
					defaultCount++
				}
			}
		}
		if defaultCount != 1 {
			t.Fatalf("found %d default integrations among the two concurrently-updated rows, want exactly 1 (update errs: %v, %v)", defaultCount, errs[0], errs[1])
		}
	})

	t.Run("Update_and_delete_integration", func(t *testing.T) {
		m := newIntegration("updatable", false)
		if err := repo.CreateIntegration(ctx, m); err != nil {
			t.Fatalf("CreateIntegration: %v", err)
		}
		m.Enabled = false
		m.ExperimentTemplate = "custom/{project_id}"
		if err := repo.UpdateIntegration(ctx, m); err != nil {
			t.Fatalf("UpdateIntegration: %v", err)
		}
		got, err := repo.GetIntegration(ctx, projectID, m.ID)
		if err != nil || got == nil {
			t.Fatalf("GetIntegration after update: %#v, err=%v", got, err)
		}
		if got.Enabled || got.ExperimentTemplate != "custom/{project_id}" {
			t.Fatalf("GetIntegration after update = %#v, want disabled+custom template", got)
		}

		if err := repo.DeleteIntegration(ctx, projectID, m.ID); err != nil {
			t.Fatalf("DeleteIntegration: %v", err)
		}
		if err := repo.DeleteIntegration(ctx, projectID, m.ID); !errors.Is(err, mlflow.ErrNotFound) {
			t.Fatalf("DeleteIntegration again: err = %v, want ErrNotFound", err)
		}
		if err := repo.UpdateIntegration(ctx, m); !errors.Is(err, mlflow.ErrNotFound) {
			t.Fatalf("UpdateIntegration after delete: err = %v, want ErrNotFound", err)
		}
	})

	// Regression for the adversarial-review finding that DeleteIntegration
	// was a hard DELETE cascading through both mapping tables' FK, silently
	// erasing experiment/run mapping history — contradicting the documented
	// contract (design doc section 11.1: "Piper→MLflow mapping 보존").
	t.Run("Delete_integration_preserves_experiment_and_run_link_mappings", func(t *testing.T) {
		m := newIntegration("mapping-survivor", false)
		if err := repo.CreateIntegration(ctx, m); err != nil {
			t.Fatalf("CreateIntegration: %v", err)
		}

		expLink := &mlflow.MLflowExperimentLink{
			IntegrationID:      m.ID,
			ProjectID:          projectID,
			PiperGroupKey:      "pipeline:survive",
			MLflowExperimentID: "42",
			MLflowName:         "piper/proj/survive",
		}
		if err := repo.UpsertExperimentLink(ctx, expLink); err != nil {
			t.Fatalf("UpsertExperimentLink: %v", err)
		}
		runID := uuid.NewString()
		runLink := &mlflow.MLflowRunLink{
			IntegrationID:      m.ID,
			ProjectID:          projectID,
			SourceType:         string(mlflow.SourceTypePipeline),
			SourceID:           runID,
			MLflowExperimentID: "42",
			MLflowRunID:        "run-survive",
			SyncStatus:         string(mlflow.SyncStatusSynced),
		}
		if err := repo.UpsertRunLink(ctx, runLink); err != nil {
			t.Fatalf("UpsertRunLink: %v", err)
		}

		if err := repo.DeleteIntegration(ctx, projectID, m.ID); err != nil {
			t.Fatalf("DeleteIntegration: %v", err)
		}

		// Both mappings must still resolve after the owning integration is
		// deleted — a hard DELETE with ON DELETE CASCADE would have erased
		// them along with the integration row.
		gotExp, err := repo.GetExperimentLink(ctx, m.ID, projectID, "pipeline:survive")
		if err != nil {
			t.Fatalf("GetExperimentLink after delete: %v", err)
		}
		if gotExp == nil || gotExp.MLflowExperimentID != "42" {
			t.Fatalf("GetExperimentLink after delete = %#v, want the mapping to survive", gotExp)
		}
		gotRun, err := repo.GetRunLink(ctx, m.ID, projectID, string(mlflow.SourceTypePipeline), runID)
		if err != nil {
			t.Fatalf("GetRunLink after delete: %v", err)
		}
		if gotRun == nil || gotRun.MLflowRunID != "run-survive" {
			t.Fatalf("GetRunLink after delete = %#v, want the mapping to survive", gotRun)
		}

		// The integration row itself survives too (soft-deleted), which is
		// what makes the FK-referencing mappings above resolvable at all —
		// GetIntegration (unlike ListIntegrations) deliberately still finds
		// it, disabled and no longer default.
		gotIntegration, err := repo.GetIntegration(ctx, projectID, m.ID)
		if err != nil {
			t.Fatalf("GetIntegration after delete: %v", err)
		}
		if gotIntegration == nil || !gotIntegration.IsDeleted() || gotIntegration.Enabled || gotIntegration.Default {
			t.Fatalf("GetIntegration after delete = %#v, want a soft-deleted, disabled, non-default row", gotIntegration)
		}

		// A soft-deleted integration must not appear in the active list...
		list, err := repo.ListIntegrations(ctx, projectID, 0, 0)
		if err != nil {
			t.Fatalf("ListIntegrations: %v", err)
		}
		for _, row := range list {
			if row.ID == m.ID {
				t.Fatalf("ListIntegrations still returned the soft-deleted integration %q", m.ID)
			}
		}

		// ...and its name must be free for a brand new integration to reuse.
		reused := newIntegration("mapping-survivor", false)
		if err := repo.CreateIntegration(ctx, reused); err != nil {
			t.Fatalf("CreateIntegration reusing a soft-deleted integration's name: %v", err)
		}
	})

	t.Run("Experiment_link_upsert_and_get", func(t *testing.T) {
		integration := newIntegration("exp-link-owner", false)
		if err := repo.CreateIntegration(ctx, integration); err != nil {
			t.Fatalf("CreateIntegration: %v", err)
		}

		missing, err := repo.GetExperimentLink(ctx, integration.ID, projectID, "pipeline:train")
		if err != nil || missing != nil {
			t.Fatalf("GetExperimentLink(missing) = %#v, err=%v, want nil, nil", missing, err)
		}

		link := &mlflow.MLflowExperimentLink{
			IntegrationID:      integration.ID,
			ProjectID:          projectID,
			PiperGroupKey:      "pipeline:train",
			MLflowExperimentID: "1",
			MLflowName:         "piper/proj/train",
		}
		if err := repo.UpsertExperimentLink(ctx, link); err != nil {
			t.Fatalf("UpsertExperimentLink: %v", err)
		}
		got, err := repo.GetExperimentLink(ctx, integration.ID, projectID, "pipeline:train")
		if err != nil || got == nil || got.MLflowExperimentID != "1" {
			t.Fatalf("GetExperimentLink = %#v, err=%v", got, err)
		}

		// Upsert again with a different MLflowExperimentID must update in place.
		link.MLflowExperimentID = "2"
		if err := repo.UpsertExperimentLink(ctx, link); err != nil {
			t.Fatalf("UpsertExperimentLink (update): %v", err)
		}
		got, err = repo.GetExperimentLink(ctx, integration.ID, projectID, "pipeline:train")
		if err != nil || got == nil || got.MLflowExperimentID != "2" {
			t.Fatalf("GetExperimentLink after update = %#v, err=%v", got, err)
		}
	})

	t.Run("Run_link_upsert_get_and_list_by_status", func(t *testing.T) {
		integration := newIntegration("run-link-owner", false)
		if err := repo.CreateIntegration(ctx, integration); err != nil {
			t.Fatalf("CreateIntegration: %v", err)
		}
		runID := uuid.NewString()

		missing, err := repo.GetRunLink(ctx, integration.ID, projectID, string(mlflow.SourceTypePipeline), runID)
		if err != nil || missing != nil {
			t.Fatalf("GetRunLink(missing) = %#v, err=%v, want nil, nil", missing, err)
		}

		link := &mlflow.MLflowRunLink{
			IntegrationID:      integration.ID,
			ProjectID:          projectID,
			SourceType:         string(mlflow.SourceTypePipeline),
			SourceID:           runID,
			MLflowExperimentID: "1",
			MLflowRunID:        "run-abc",
			SyncStatus:         string(mlflow.SyncStatusPending),
		}
		if err := repo.UpsertRunLink(ctx, link); err != nil {
			t.Fatalf("UpsertRunLink: %v", err)
		}
		got, err := repo.GetRunLink(ctx, integration.ID, projectID, string(mlflow.SourceTypePipeline), runID)
		if err != nil || got == nil || got.SyncStatus != string(mlflow.SyncStatusPending) {
			t.Fatalf("GetRunLink = %#v, err=%v", got, err)
		}

		link.SyncStatus = string(mlflow.SyncStatusSynced)
		link.LastSequence = 3
		if err := repo.UpsertRunLink(ctx, link); err != nil {
			t.Fatalf("UpsertRunLink (update): %v", err)
		}

		pending, err := repo.ListRunLinksByStatus(ctx, projectID, string(mlflow.SyncStatusPending), 0, 0)
		if err != nil {
			t.Fatalf("ListRunLinksByStatus(pending): %v", err)
		}
		for _, l := range pending {
			if l.IntegrationID == integration.ID && l.SourceID == runID {
				t.Fatalf("run link still listed under pending after moving to synced: %#v", l)
			}
		}

		synced, err := repo.ListRunLinksByStatus(ctx, projectID, string(mlflow.SyncStatusSynced), 0, 0)
		if err != nil {
			t.Fatalf("ListRunLinksByStatus(synced): %v", err)
		}
		found := false
		for _, l := range synced {
			if l.IntegrationID == integration.ID && l.SourceID == runID {
				found = true
				if l.LastSequence != 3 {
					t.Fatalf("run link LastSequence = %d, want 3", l.LastSequence)
				}
			}
		}
		if !found {
			t.Fatalf("run link not found under synced status")
		}
	})
}

// TemplateRepoSuite exercises template.Repository's Create/Get/List contract,
// with particular attention to the StorageBackend stamp (see
// Template.StorageBackend / storageIdentity() in piper's settings.go)
// round-tripping correctly through both driver implementations.
func TemplateRepoSuite(t *testing.T, repo template.Repository, projectID string) {
	t.Helper()
	ctx := context.Background()

	t.Run("Create_and_Get", func(t *testing.T) {
		tmpl := &template.Template{
			ProjectID: projectID,
			Name:      "storage-backend-test",
			Version:   1,
			YAML:      "metadata:\n  name: storage-backend-test\n",
			Tags:      []string{},
		}
		if err := repo.Create(ctx, tmpl); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if tmpl.ID == "" {
			t.Fatal("Create did not assign an ID")
		}
		got, err := repo.Get(ctx, projectID, tmpl.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Name != tmpl.Name || got.Version != tmpl.Version {
			t.Errorf("Get = %+v, want Name=%q Version=%d", got, tmpl.Name, tmpl.Version)
		}
	})

	t.Run("StorageBackend_roundtrips_through_Create_Get_and_List", func(t *testing.T) {
		tmpl := &template.Template{
			ProjectID:      projectID,
			Name:           "storage-backend-stamped",
			Version:        1,
			YAML:           "metadata:\n  name: storage-backend-stamped\n",
			Tags:           []string{},
			SnapshotID:     "snap-1",
			StorageBackend: "s3:my-bucket",
		}
		if err := repo.Create(ctx, tmpl); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := repo.Get(ctx, projectID, tmpl.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.StorageBackend != "s3:my-bucket" {
			t.Errorf("Get: StorageBackend = %q, want %q", got.StorageBackend, "s3:my-bucket")
		}

		rows, err := repo.List(ctx, projectID, template.Filter{Name: "storage-backend-stamped"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		found := false
		for _, row := range rows {
			if row.ID == tmpl.ID {
				found = true
				if row.StorageBackend != "s3:my-bucket" {
					t.Errorf("List: StorageBackend = %q, want %q", row.StorageBackend, "s3:my-bucket")
				}
			}
		}
		if !found {
			t.Fatalf("List did not return created template %q", tmpl.ID)
		}

		// A template created without an explicit StorageBackend (the common
		// case for rows written before this field existed, or a submit with
		// no local-source steps) must round-trip as "" — the read-time
		// mismatch check treats "" as "unknown, don't flag".
		unstamped := &template.Template{
			ProjectID: projectID,
			Name:      "storage-backend-unstamped",
			Version:   1,
			YAML:      "metadata:\n  name: storage-backend-unstamped\n",
			Tags:      []string{},
		}
		if err := repo.Create(ctx, unstamped); err != nil {
			t.Fatalf("Create (unstamped): %v", err)
		}
		gotUnstamped, err := repo.Get(ctx, projectID, unstamped.ID)
		if err != nil {
			t.Fatalf("Get (unstamped): %v", err)
		}
		if gotUnstamped.StorageBackend != "" {
			t.Errorf("Get (unstamped): StorageBackend = %q, want empty", gotUnstamped.StorageBackend)
		}
	})
}

// OutboxRepoSuite exercises outbox.Repository: auto-assigned per-aggregate
// sequence, the claim/lease/reclaim lifecycle, the per-aggregate ordering
// gate (design doc section 10.3 — a later-sequence event is never claimable
// while an earlier one for the same aggregate is still pending/delivering,
// but a StatusDead earlier event does not block a later one — see
// pkg/integration/mlflow/exporter.go's handlePipelineRunFinished doc
// comment for why that's the deliberate, documented behavior), dead-letter,
// and DisableIntegrationEvents (design doc section 11.1's integration
// delete semantics). mlflowRepo is used only to create the owning
// MLflowIntegration row the outbox table's FK requires — this suite does
// not otherwise exercise mlflow.Repository.
func OutboxRepoSuite(t *testing.T, outboxRepo outbox.Repository, mlflowRepo mlflow.Repository, projectID string) {
	t.Helper()
	ctx := context.Background()

	newIntegration := func(t *testing.T) *mlflow.MLflowIntegration {
		t.Helper()
		m := &mlflow.MLflowIntegration{
			ID:                 uuid.NewString(),
			ProjectID:          projectID,
			Name:               "outbox-" + uuid.NewString(),
			TrackingURI:        "https://mlflow.example.com",
			CredentialRef:      "mlflow-cred",
			Enabled:            true,
			ExperimentTemplate: mlflow.DefaultExperimentTemplate,
			ArtifactMode:       string(mlflow.ArtifactModeReference),
		}
		if err := mlflowRepo.CreateIntegration(ctx, m); err != nil {
			t.Fatalf("CreateIntegration (fixture): %v", err)
		}
		return m
	}

	newEvent := func(integrationID, aggregateID, eventType string) *outbox.Event {
		return &outbox.Event{
			ID:            uuid.NewString(),
			IntegrationID: integrationID,
			ProjectID:     projectID,
			AggregateType: outbox.AggregateTypePipelineRun,
			AggregateID:   aggregateID,
			EventType:     eventType,
			PayloadJSON:   []byte(`{"k":"v"}`),
		}
	}

	containsID := func(events []*outbox.Event, id string) bool {
		for _, e := range events {
			if e.ID == id {
				return true
			}
		}
		return false
	}

	t.Run("Enqueue_auto_assigns_sequence_and_claim_enforces_ordering_gate", func(t *testing.T) {
		integration := newIntegration(t)
		aggID := uuid.NewString()
		created := newEvent(integration.ID, aggID, "pipeline_run.created")
		if err := outboxRepo.Enqueue(ctx, created); err != nil {
			t.Fatalf("Enqueue created: %v", err)
		}
		if created.Sequence != 1 {
			t.Fatalf("created.Sequence = %d, want 1", created.Sequence)
		}
		if created.Status != string(outbox.StatusPending) {
			t.Fatalf("created.Status = %q, want pending", created.Status)
		}
		finished := newEvent(integration.ID, aggID, "pipeline_run.finished")
		if err := outboxRepo.Enqueue(ctx, finished); err != nil {
			t.Fatalf("Enqueue finished: %v", err)
		}
		if finished.Sequence != 2 {
			t.Fatalf("finished.Sequence = %d, want 2", finished.Sequence)
		}

		claimed, err := outboxRepo.ClaimBatch(ctx, "worker-a", time.Minute, 10)
		if err != nil {
			t.Fatalf("ClaimBatch: %v", err)
		}
		if !containsID(claimed, created.ID) {
			t.Fatalf("ClaimBatch did not claim the lowest-sequence event %q; claimed=%v", created.ID, ids(claimed))
		}
		if containsID(claimed, finished.ID) {
			t.Fatalf("ClaimBatch claimed the later-sequence event %q while the earlier one is still delivering — ordering gate violated", finished.ID)
		}

		if err := outboxRepo.MarkDelivered(ctx, created.ID, "worker-a"); err != nil {
			t.Fatalf("MarkDelivered created: %v", err)
		}

		claimed2, err := outboxRepo.ClaimBatch(ctx, "worker-a", time.Minute, 10)
		if err != nil {
			t.Fatalf("ClaimBatch (2nd): %v", err)
		}
		if !containsID(claimed2, finished.ID) {
			t.Fatalf("finished event not claimable after the earlier event was delivered; claimed=%v", ids(claimed2))
		}
		if err := outboxRepo.MarkDelivered(ctx, finished.ID, "worker-a"); err != nil {
			t.Fatalf("MarkDelivered finished: %v", err)
		}
	})

	t.Run("Dead_earlier_event_does_not_block_a_later_one", func(t *testing.T) {
		integration := newIntegration(t)
		aggID := uuid.NewString()
		e1 := newEvent(integration.ID, aggID, "pipeline_run.created")
		e2 := newEvent(integration.ID, aggID, "pipeline_run.finished")
		if err := outboxRepo.Enqueue(ctx, e1); err != nil {
			t.Fatalf("Enqueue e1: %v", err)
		}
		if err := outboxRepo.Enqueue(ctx, e2); err != nil {
			t.Fatalf("Enqueue e2: %v", err)
		}

		claimed, err := outboxRepo.ClaimBatch(ctx, "worker-a", time.Minute, 10)
		if err != nil {
			t.Fatalf("ClaimBatch: %v", err)
		}
		if !containsID(claimed, e1.ID) {
			t.Fatalf("expected e1 claimed first; claimed=%v", ids(claimed))
		}
		if err := outboxRepo.MarkDead(ctx, e1.ID, "worker-a", "validation_error", "bad payload"); err != nil {
			t.Fatalf("MarkDead e1: %v", err)
		}

		claimed2, err := outboxRepo.ClaimBatch(ctx, "worker-a", time.Minute, 10)
		if err != nil {
			t.Fatalf("ClaimBatch (2nd): %v", err)
		}
		if !containsID(claimed2, e2.ID) {
			t.Fatalf("e2 should become claimable once e1 is dead (dead events don't hold the ordering gate); claimed=%v", ids(claimed2))
		}
		if err := outboxRepo.MarkDelivered(ctx, e2.ID, "worker-a"); err != nil {
			t.Fatalf("MarkDelivered e2: %v", err)
		}
	})

	t.Run("Expired_lease_is_reclaimed_by_another_owner", func(t *testing.T) {
		integration := newIntegration(t)
		ev := newEvent(integration.ID, uuid.NewString(), "pipeline_run.created")
		if err := outboxRepo.Enqueue(ctx, ev); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		claimed, err := outboxRepo.ClaimBatch(ctx, "worker-a", time.Millisecond, 10)
		if err != nil {
			t.Fatalf("ClaimBatch: %v", err)
		}
		if !containsID(claimed, ev.ID) {
			t.Fatalf("expected initial claim to succeed; claimed=%v", ids(claimed))
		}
		time.Sleep(5 * time.Millisecond) // let the 1ms lease expire

		reclaimed, err := outboxRepo.ClaimBatch(ctx, "worker-b", time.Minute, 10)
		if err != nil {
			t.Fatalf("ClaimBatch (reclaim): %v", err)
		}
		var got *outbox.Event
		for _, e := range reclaimed {
			if e.ID == ev.ID {
				got = e
			}
		}
		if got == nil {
			t.Fatalf("expired lease was not reclaimed by worker-b; reclaimed=%v", ids(reclaimed))
		}
		if got.Attempts != 2 {
			t.Errorf("Attempts = %d, want 2 (claimed twice)", got.Attempts)
		}
		// The original owner can no longer resolve it — its lease is gone.
		if err := outboxRepo.MarkDelivered(ctx, ev.ID, "worker-a"); !errors.Is(err, outbox.ErrNotFound) {
			t.Errorf("MarkDelivered by the original (reclaimed-from) owner: err = %v, want ErrNotFound", err)
		}
		if err := outboxRepo.MarkDelivered(ctx, ev.ID, "worker-b"); err != nil {
			t.Fatalf("MarkDelivered by worker-b: %v", err)
		}
	})

	t.Run("MarkRetry_returns_to_pending_and_honors_next_attempt_at", func(t *testing.T) {
		integration := newIntegration(t)

		// Case 1: retry scheduled in the future must not be claimable yet.
		notDueEvent := newEvent(integration.ID, uuid.NewString(), "pipeline_run.created")
		if err := outboxRepo.Enqueue(ctx, notDueEvent); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		claimed, err := outboxRepo.ClaimBatch(ctx, "worker-a", time.Minute, 10)
		if err != nil || !containsID(claimed, notDueEvent.ID) {
			t.Fatalf("initial claim failed: claimed=%v err=%v", ids(claimed), err)
		}
		future := time.Now().UTC().Add(time.Hour)
		if err := outboxRepo.MarkRetry(ctx, notDueEvent.ID, "worker-a", future, "HTTP_503", "temporarily unavailable"); err != nil {
			t.Fatalf("MarkRetry: %v", err)
		}
		notYet, err := outboxRepo.ClaimBatch(ctx, "worker-a", time.Minute, 10)
		if err != nil {
			t.Fatalf("ClaimBatch (not due): %v", err)
		}
		if containsID(notYet, notDueEvent.ID) {
			t.Fatalf("event claimed before its NextAttemptAt was due")
		}
		// MarkRetry only applies to a currently-delivering (claimed) event;
		// the event above is back to StatusPending, so a second MarkRetry
		// against it must fail rather than silently reschedule a
		// not-actually-in-flight event.
		if err := outboxRepo.MarkRetry(ctx, notDueEvent.ID, "worker-a", time.Now().UTC(), "HTTP_503", "still unavailable"); !errors.Is(err, outbox.ErrNotFound) {
			t.Fatalf("MarkRetry on a non-delivering (already-pending) event: err = %v, want ErrNotFound", err)
		}

		// Case 2: retry scheduled at (or before) now must be immediately
		// reclaimable.
		dueEvent := newEvent(integration.ID, uuid.NewString(), "pipeline_run.created")
		if err := outboxRepo.Enqueue(ctx, dueEvent); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		if _, err := outboxRepo.ClaimBatch(ctx, "worker-a", time.Minute, 10); err != nil {
			t.Fatalf("ClaimBatch: %v", err)
		}
		if err := outboxRepo.MarkRetry(ctx, dueEvent.ID, "worker-a", time.Now().UTC(), "HTTP_503", "temporarily unavailable"); err != nil {
			t.Fatalf("MarkRetry (due now): %v", err)
		}
		reclaimed, err := outboxRepo.ClaimBatch(ctx, "worker-b", time.Minute, 10)
		if err != nil {
			t.Fatalf("ClaimBatch (reclaim after due retry): %v", err)
		}
		if !containsID(reclaimed, dueEvent.ID) {
			t.Fatalf("event scheduled for now was not immediately reclaimable; claimed=%v", ids(reclaimed))
		}
		if err := outboxRepo.MarkDelivered(ctx, dueEvent.ID, "worker-b"); err != nil {
			t.Fatalf("MarkDelivered: %v", err)
		}
		// Hygiene: resolve the still-future-scheduled event too, so it
		// doesn't linger as a permanently-pending row for the rest of this
		// suite's shared DB.
		if err := outboxRepo.MarkDead(ctx, notDueEvent.ID, "worker-a", "test_cleanup", "unused after future MarkRetry assertion"); err == nil {
			t.Fatalf("MarkDead on a pending (not delivering) event unexpectedly succeeded")
		}
	})

	t.Run("DisableIntegrationEvents_transitions_pending_and_delivering_only", func(t *testing.T) {
		integration := newIntegration(t)
		aggID := uuid.NewString()
		pending := newEvent(integration.ID, aggID, "pipeline_run.created")
		if err := outboxRepo.Enqueue(ctx, pending); err != nil {
			t.Fatalf("Enqueue pending: %v", err)
		}
		delivered := newEvent(integration.ID, uuid.NewString(), "pipeline_run.created")
		if err := outboxRepo.Enqueue(ctx, delivered); err != nil {
			t.Fatalf("Enqueue delivered: %v", err)
		}
		if _, err := outboxRepo.ClaimBatch(ctx, "worker-a", time.Minute, 10); err != nil {
			t.Fatalf("ClaimBatch: %v", err)
		}
		if err := outboxRepo.MarkDelivered(ctx, delivered.ID, "worker-a"); err != nil {
			t.Fatalf("MarkDelivered: %v", err)
		}

		n, err := outboxRepo.DisableIntegrationEvents(ctx, integration.ID)
		if err != nil {
			t.Fatalf("DisableIntegrationEvents: %v", err)
		}
		if n != 1 {
			t.Fatalf("DisableIntegrationEvents transitioned %d rows, want 1 (only the still-pending one)", n)
		}

		rows, err := outboxRepo.ListByAggregate(ctx, integration.ID, outbox.AggregateTypePipelineRun, aggID)
		if err != nil {
			t.Fatalf("ListByAggregate: %v", err)
		}
		if len(rows) != 1 || rows[0].Status != string(outbox.StatusDisabled) {
			t.Fatalf("ListByAggregate after disable = %+v, want one StatusDisabled row", rows)
		}

		// Idempotent: disabling again transitions nothing further.
		n2, err := outboxRepo.DisableIntegrationEvents(ctx, integration.ID)
		if err != nil {
			t.Fatalf("DisableIntegrationEvents (2nd): %v", err)
		}
		if n2 != 0 {
			t.Fatalf("DisableIntegrationEvents (2nd) transitioned %d rows, want 0", n2)
		}
	})

	t.Run("CountByStatus_and_OldestPending", func(t *testing.T) {
		integration := newIntegration(t)
		before := time.Now().UTC()
		ev := newEvent(integration.ID, uuid.NewString(), "pipeline_run.created")
		if err := outboxRepo.Enqueue(ctx, ev); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}

		pendingCount, err := outboxRepo.CountByStatus(ctx, integration.ID, string(outbox.StatusPending))
		if err != nil {
			t.Fatalf("CountByStatus pending: %v", err)
		}
		if pendingCount != 1 {
			t.Fatalf("CountByStatus pending = %d, want 1", pendingCount)
		}

		oldest, err := outboxRepo.OldestPending(ctx, integration.ID)
		if err != nil {
			t.Fatalf("OldestPending: %v", err)
		}
		if oldest == nil || oldest.Before(before.Add(-time.Second)) {
			t.Fatalf("OldestPending = %v, want a timestamp near %v", oldest, before)
		}

		if _, err := outboxRepo.ClaimBatch(ctx, "worker-a", time.Minute, 10); err != nil {
			t.Fatalf("ClaimBatch: %v", err)
		}
		if err := outboxRepo.MarkDead(ctx, ev.ID, "worker-a", "invalid_payload", "bad"); err != nil {
			t.Fatalf("MarkDead: %v", err)
		}
		deadCount, err := outboxRepo.CountByStatus(ctx, integration.ID, string(outbox.StatusDead))
		if err != nil {
			t.Fatalf("CountByStatus dead: %v", err)
		}
		if deadCount != 1 {
			t.Fatalf("CountByStatus dead = %d, want 1", deadCount)
		}
		oldestAfter, err := outboxRepo.OldestPending(ctx, integration.ID)
		if err != nil {
			t.Fatalf("OldestPending (after dead): %v", err)
		}
		if oldestAfter != nil {
			t.Fatalf("OldestPending after the only event went dead = %v, want nil", oldestAfter)
		}
	})

	t.Run("Backlog", func(t *testing.T) {
		integrationA := newIntegration(t)
		integrationB := newIntegration(t)
		integrationEmpty := newIntegration(t)
		before := time.Now().UTC()

		// deadB is enqueued and driven to StatusDead before pendingA is even
		// enqueued, so ClaimBatch (which claims across all integrations, not
		// just integrationB) cannot also sweep up pendingA.
		deadB := newEvent(integrationB.ID, uuid.NewString(), "pipeline_run.created")
		if err := outboxRepo.Enqueue(ctx, deadB); err != nil {
			t.Fatalf("Enqueue deadB: %v", err)
		}
		if _, err := outboxRepo.ClaimBatch(ctx, "worker-backlog", time.Minute, 10); err != nil {
			t.Fatalf("ClaimBatch: %v", err)
		}
		if err := outboxRepo.MarkDead(ctx, deadB.ID, "worker-backlog", "invalid_payload", "bad"); err != nil {
			t.Fatalf("MarkDead: %v", err)
		}

		pendingA := newEvent(integrationA.ID, uuid.NewString(), "pipeline_run.created")
		if err := outboxRepo.Enqueue(ctx, pendingA); err != nil {
			t.Fatalf("Enqueue pendingA: %v", err)
		}

		backlog, err := outboxRepo.Backlog(ctx, []string{integrationA.ID, integrationB.ID, integrationEmpty.ID})
		if err != nil {
			t.Fatalf("Backlog: %v", err)
		}
		bA := backlog[integrationA.ID]
		if bA.Pending != 1 || bA.Dead != 0 {
			t.Fatalf("Backlog[A] = %+v, want Pending=1 Dead=0", bA)
		}
		if bA.OldestPending == nil || bA.OldestPending.Before(before.Add(-time.Second)) {
			t.Fatalf("Backlog[A].OldestPending = %v, want a timestamp near %v", bA.OldestPending, before)
		}
		bB := backlog[integrationB.ID]
		if bB.Pending != 0 || bB.Dead != 1 {
			t.Fatalf("Backlog[B] = %+v, want Pending=0 Dead=1", bB)
		}
		if bB.OldestPending != nil {
			t.Fatalf("Backlog[B].OldestPending = %v, want nil (only event is dead)", bB.OldestPending)
		}
		if bEmpty, ok := backlog[integrationEmpty.ID]; ok && (bEmpty.Pending != 0 || bEmpty.Dead != 0 || bEmpty.OldestPending != nil) {
			t.Fatalf("Backlog[empty] = %+v, want zero value or absent", bEmpty)
		}
	})
}

func ids(events []*outbox.Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.ID
	}
	return out
}
