package store

import (
	"context"
	"testing"

	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/project"
)

// The failure reason is stored with the status, replaced by the next status
// write, and kept in history when a failed notebook is deleted.
func TestNotebookRepo_ReasonRoundTrip(t *testing.T) {
	ctx := context.Background()
	repos, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repos.Close() })

	const projectID = "notebook-reason-project"
	if err := repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	if err := repos.Notebook.Create(ctx, &notebook.NotebookServer{ProjectID: projectID, Name: "nb", Status: notebook.StatusStarting}); err != nil {
		t.Fatal(err)
	}

	reason := `k8sdriver: namespace "notebooks" is not allowed`
	if err := repos.Notebook.SetStatus(ctx, projectID, "nb", notebook.StatusFailed, reason); err != nil {
		t.Fatal(err)
	}
	got, err := repos.Notebook.Get(ctx, projectID, "nb")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != notebook.StatusFailed || got.Reason != reason {
		t.Fatalf("status/reason = %q/%q", got.Status, got.Reason)
	}

	if err := repos.Notebook.SetStatus(ctx, projectID, "nb", notebook.StatusStarting, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := repos.Notebook.Get(ctx, projectID, "nb"); got.Reason != "" {
		t.Fatalf("reason = %q after restart, want empty", got.Reason)
	}
	if _, err := repos.Notebook.FinishRun(ctx, projectID, "nb", notebook.StatusFailed, reason); err != nil {
		t.Fatal(err)
	}
	hist, err := repos.Notebook.ListHistory(ctx, projectID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || hist[0].Reason != reason {
		t.Fatalf("history = %#v, want the failure reason kept", hist)
	}
}
