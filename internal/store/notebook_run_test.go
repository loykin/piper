package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/pressly/goose/v3"

	"github.com/loykin/piper/internal/store/repotest"
	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/project"
)

// recordNotebookRun creates a running notebook and ends its run, which writes
// one notebook_history row.
func recordNotebookRun(t *testing.T, repos *Repos, projectID, name string) {
	t.Helper()
	ctx := context.Background()
	if err := repos.Notebook.Create(ctx, &notebook.NotebookServer{ProjectID: projectID, Name: name, Status: notebook.StatusRunning}); err != nil {
		t.Fatal(err)
	}
	if finished, err := repos.Notebook.FinishRun(ctx, projectID, name, notebook.StatusStopped, ""); err != nil || !finished {
		t.Fatalf("FinishRun = %v, %v; want the run ended", finished, err)
	}
}

func TestNotebookRepo_Runs(t *testing.T) {
	ctx := context.Background()
	repos, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	const projectID = "notebook-run-project"
	if err := repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
		t.Fatal(err)
	}
	repotest.NotebookRunSuite(t, repos.Notebook, projectID)
}

// Migration 00050 gives existing notebooks a started_at and records the last
// run of every notebook that had already ended, which the new rule (record
// when a run ends) would otherwise never archive.
func TestMigration00050_BackfillsEndedNotebooks(t *testing.T) {
	ctx := context.Background()
	db, err := sqlx.Connect("sqlite", filepath.Join(t.TempDir(), "m.db")+"?_foreign_keys=on")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	goose.SetBaseFS(sqliteMigrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpToContext(ctx, db.DB, "migrations/sqlite", 49); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	stopped := created.Add(time.Hour)
	if _, err := db.ExecContext(ctx, `INSERT INTO projects (id, name, created_at, updated_at) VALUES ('p', 'p', ?, ?)`, created, created); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ name, status string }{{"ended", "stopped"}, {"crashed", "failed"}, {"live", "running"}} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO notebook_servers (project_id, name, status, created_at, updated_at) VALUES ('p', ?, ?, ?, ?)`,
			row.name, row.status, created, stopped); err != nil {
			t.Fatal(err)
		}
	}
	if err := goose.UpToContext(ctx, db.DB, "migrations/sqlite", 50); err != nil {
		t.Fatal(err)
	}

	var startedAt []time.Time
	if err := db.SelectContext(ctx, &startedAt, `SELECT started_at FROM notebook_servers`); err != nil {
		t.Fatal(err)
	}
	for _, s := range startedAt {
		if !s.Equal(created) {
			t.Fatalf("started_at = %v, want created_at %v", s, created)
		}
	}
	var hist []struct {
		Name       string    `db:"name"`
		Status     string    `db:"status"`
		DeployedAt time.Time `db:"deployed_at"`
		StoppedAt  time.Time `db:"stopped_at"`
	}
	if err := db.SelectContext(ctx, &hist, `SELECT name, status, deployed_at, stopped_at FROM notebook_history ORDER BY name`); err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 || hist[0].Name != "crashed" || hist[0].Status != "failed" || hist[1].Name != "ended" ||
		!hist[1].DeployedAt.Equal(created) || !hist[1].StoppedAt.Equal(stopped) {
		t.Fatalf("backfilled history = %+v, want the stopped and failed notebooks only", hist)
	}
}
