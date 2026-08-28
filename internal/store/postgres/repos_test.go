//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/loykin/piper/internal/store"
	"github.com/loykin/piper/internal/store/repotest"
	"github.com/loykin/piper/pkg/project"
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

func TestPipelineTemplateRepo_Postgres(t *testing.T) {
	ctx := context.Background()
	repos := openPostgresRepos(t, ctx)

	const projectID = "template-repo"
	if err := repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	repotest.TemplateRepoSuite(t, repos.PipelineTemplate, projectID)
}

func TestProjectRepo_Postgres(t *testing.T) {
	ctx := context.Background()
	repos := openPostgresRepos(t, ctx)

	repotest.ProjectRepoSuite(t, repos.Project)
}

func TestFederationRepo_Postgres(t *testing.T) {
	ctx := context.Background()
	repos := openPostgresRepos(t, ctx)
	repotest.FederationRepoSuite(t, repos.Federation)
}

func TestSubmissionRepo_Postgres(t *testing.T) {
	ctx := context.Background()
	repos := openPostgresRepos(t, ctx)
	const projectID = "submission-repo"
	if err := repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	repotest.SubmissionRepoSuite(t, repos.Submission, projectID)
}

func TestProjectMutationRepo_Postgres(t *testing.T) {
	ctx := context.Background()
	repos := openPostgresRepos(t, ctx)
	const projectID = "project-mutation-repo"
	if err := repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	repotest.ProjectMutationRepoSuite(t, repos.ProjectMutation, projectID)
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

func TestCredentialRepo_Postgres(t *testing.T) {
	ctx := context.Background()
	repos := openPostgresRepos(t, ctx)

	const projectID = "secret-repo"
	if err := repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	repotest.CredentialRepoSuite(t, repos.Credential, projectID)
}

func TestMlflowRepo_Postgres(t *testing.T) {
	ctx := context.Background()
	repos := openPostgresRepos(t, ctx)

	const projectID = "mlflow-repo"
	if err := repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	repotest.MLflowRepoSuite(t, repos.Mlflow, projectID)
}
