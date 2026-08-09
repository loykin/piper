package store

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

// TestSQLiteMigrationDownThenUpDoesNotFail guards against a class of bug
// where a migration's "Down" leaves a column in place (e.g. because an
// older, pre-existing migration in this repo couldn't drop columns on an
// older SQLite): goose still records the version as rolled back, so the
// next "up" tries to re-add a column that's still there and fails with
// "duplicate column name". This exercises the real goose runner (not just a
// raw db.Exec check) against the actual embedded migration files.
func TestSQLiteMigrationDownThenUpDoesNotFail(t *testing.T) {
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if err := migrate(ctx, db, "sqlite"); err != nil {
		t.Fatalf("initial migrate up: %v", err)
	}

	goose.SetBaseFS(sqliteMigrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	dir := "migrations/sqlite"

	if err := goose.DownContext(ctx, db.DB, dir); err != nil {
		t.Fatalf("goose down (rolling back the latest migration): %v", err)
	}
	if err := goose.UpContext(ctx, db.DB, dir); err != nil {
		t.Fatalf("goose up after down: %v (a migration's Down likely left a column/table in place that Up then tried to recreate)", err)
	}
}
