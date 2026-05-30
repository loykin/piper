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
