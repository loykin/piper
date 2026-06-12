package store

import (
	"context"
	"embed"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/sqlite/*.sql
var sqliteMigrationsFS embed.FS

//go:embed migrations/postgres/*.sql
var postgresMigrationsFS embed.FS

// Migrate runs all pending database migrations for the given driver.
// This is exported so that admin CLI commands (e.g. piper user) can bootstrap
// a fresh database before operating on it.
func Migrate(ctx context.Context, db *sqlx.DB, driver string) error {
	return migrate(ctx, db, driver)
}

func migrate(ctx context.Context, db *sqlx.DB, driver string) error {
	var fs embed.FS
	var dir, dialect string
	switch driver {
	case "sqlite", "sqlite3", "":
		fs, dir, dialect = sqliteMigrationsFS, "migrations/sqlite", "sqlite3"
	case "postgres", "postgresql":
		fs, dir, dialect = postgresMigrationsFS, "migrations/postgres", "postgres"
	default:
		return fmt.Errorf("unsupported db driver: %s", driver)
	}

	goose.SetBaseFS(fs)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect(dialect); err != nil {
		return fmt.Errorf("goose set dialect: %w", err)
	}
	if err := goose.RunContext(ctx, "up", db.DB, dir); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}
