package sqlite_test

import (
	"testing"

	"github.com/piper/piper/internal/store"
	"github.com/piper/piper/internal/store/repotest"
)

func TestRunRepo_SQLite(t *testing.T) {
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	repotest.RunRepoSuite(t, repos.Run)
}

func TestStepRepo_SQLite(t *testing.T) {
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	repotest.StepRepoSuite(t, repos.Step)
}
