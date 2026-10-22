package store

import (
	"context"
	"testing"

	"github.com/loykin/piper/internal/store/repotest"
	"github.com/loykin/piper/pkg/project"
)

func TestServingRepo_SetRuntime(t *testing.T) {
	ctx := context.Background()
	repos, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repos.Close() })

	const projectID = "serving-runtime"
	if err := repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	repotest.ServingRuntimeSuite(t, repos.Serving, projectID)
}
