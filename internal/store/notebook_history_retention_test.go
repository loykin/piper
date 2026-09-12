package store

import (
	"context"
	"testing"
	"time"

	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/project"
)

func TestNotebookRepo_PurgeHistoryBefore(t *testing.T) {
	ctx := context.Background()
	repos, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repos.Close() })

	const projectID = "notebook-history-retention-project"
	if err := repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}

	old := &notebook.NotebookServer{ProjectID: projectID, Name: "old-nb", Status: "stopped", CreatedAt: time.Now().UTC()}
	recent := &notebook.NotebookServer{ProjectID: projectID, Name: "recent-nb", Status: "stopped", CreatedAt: time.Now().UTC()}
	if err := repos.Notebook.AppendHistory(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := repos.Notebook.AppendHistory(ctx, recent); err != nil {
		t.Fatal(err)
	}

	// AppendHistory always stamps stopped_at with time.Now(); backdate the
	// "old" row directly so the TTL cutoff actually has something to catch.
	backdated := time.Now().UTC().Add(-48 * time.Hour)
	if _, err := repos.DB().ExecContext(ctx, `UPDATE notebook_history SET stopped_at = ? WHERE name = ?`, backdated, "old-nb"); err != nil {
		t.Fatal(err)
	}

	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	removed, err := repos.Notebook.PurgeHistoryBefore(ctx, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 row removed, got %d", removed)
	}

	hist, err := repos.Notebook.ListHistory(ctx, projectID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || hist[0].Name != "recent-nb" {
		t.Fatalf("expected only recent-nb to survive, got %#v", hist)
	}
}
