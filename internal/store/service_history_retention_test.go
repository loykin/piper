package store

import (
	"context"
	"testing"
	"time"

	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/serving"
)

func TestServingRepo_PurgeHistoryBefore(t *testing.T) {
	ctx := context.Background()
	repos, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repos.Close() })

	const projectID = "service-history-retention-project"
	if err := repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}

	old := &serving.Service{ProjectID: projectID, Name: "old-svc", Status: "stopped", CreatedAt: time.Now().UTC()}
	recent := &serving.Service{ProjectID: projectID, Name: "recent-svc", Status: "stopped", CreatedAt: time.Now().UTC()}
	if err := repos.Serving.AppendHistory(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := repos.Serving.AppendHistory(ctx, recent); err != nil {
		t.Fatal(err)
	}

	// AppendHistory always stamps stopped_at with time.Now(); backdate the
	// "old" row directly so the TTL cutoff actually has something to catch.
	backdated := time.Now().UTC().Add(-48 * time.Hour)
	if _, err := repos.DB().ExecContext(ctx, `UPDATE service_history SET stopped_at = ? WHERE name = ?`, backdated, "old-svc"); err != nil {
		t.Fatal(err)
	}

	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	removed, err := repos.Serving.PurgeHistoryBefore(ctx, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 row removed, got %d", removed)
	}

	hist, err := repos.Serving.ListHistory(ctx, projectID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || hist[0].Name != "recent-svc" {
		t.Fatalf("expected only recent-svc to survive, got %#v", hist)
	}
}
