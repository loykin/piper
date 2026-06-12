package sqlite_test

import (
	"context"
	"testing"

	"github.com/piper/piper/internal/store"
	"github.com/piper/piper/internal/store/repotest"
	"github.com/piper/piper/pkg/project"
)

func TestRunRepo_SQLite(t *testing.T) {
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	const projectID = "run-repo"
	if err := repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	repotest.RunRepoSuite(t, repos.Run, projectID)
}

func TestProjectRepo_SQLite(t *testing.T) {
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	repotest.ProjectRepoSuite(t, repos.Project)
}

func TestStepRepo_SQLite(t *testing.T) {
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	const projectID = "step-repo"
	if err := repos.Project.Create(context.Background(), &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	repotest.StepRepoSuite(t, repos.Step, projectID)
}
