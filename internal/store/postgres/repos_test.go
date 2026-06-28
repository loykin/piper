//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/piper/piper/internal/store"
	"github.com/piper/piper/internal/store/repotest"
	"github.com/piper/piper/pkg/project"
)

func openPostgresRepos(t *testing.T, ctx context.Context) *store.Repos {
	t.Helper()

	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("piper_test"),
		tcpostgres.WithUsername("piper"),
		tcpostgres.WithPassword("piper"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	repos, err := store.OpenPostgres(dsn)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	return repos
}

func TestRunRepo_Postgres(t *testing.T) {
	ctx := context.Background()
	repos := openPostgresRepos(t, ctx)

	const projectID = "run-repo"
	if err := repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	repotest.RunRepoSuite(t, repos.Run, projectID)
}

func TestProjectRepo_Postgres(t *testing.T) {
	ctx := context.Background()
	repos := openPostgresRepos(t, ctx)

	repotest.ProjectRepoSuite(t, repos.Project)
}

func TestStepRepo_Postgres(t *testing.T) {
	ctx := context.Background()
	repos := openPostgresRepos(t, ctx)

	const projectID = "step-repo"
	if err := repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	repotest.StepRepoSuite(t, repos.Step, projectID)
}
