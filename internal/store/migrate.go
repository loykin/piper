package store

import (
	"context"
	"embed"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func migrate(ctx context.Context, db *sqlx.DB, driver string) error {
	switch driver {
	case "sqlite", "sqlite3", "":
		if err := goose.SetDialect("sqlite3"); err != nil {
			return fmt.Errorf("goose set dialect: %w", err)
		}
	default:
		return fmt.Errorf("unsupported db driver: %s", driver)
	}

	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())

	if err := goose.RunContext(ctx, "up", db.DB, "migrations"); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}
