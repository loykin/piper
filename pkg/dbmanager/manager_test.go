package dbmanager

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
)

func TestManagerDeduplicatesByKey(t *testing.T) {
	mgr := New()
	dir := t.TempDir()
	spec := Spec{
		Name:   "sqlite-main",
		Driver: DriverSQLite,
		DSN:    filepath.Join(dir, "manager.db"),
	}

	db1, err := mgr.Get(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	db2, err := mgr.Get(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if db1 != db2 {
		t.Fatal("expected cached DB handle to be reused")
	}

	stats, ok := mgr.Stats(spec)
	if !ok {
		t.Fatal("expected stats for cached DB")
	}
	if stats.MaxOpenConnections != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", stats.MaxOpenConnections)
	}

	if err := mgr.Remove(spec); err != nil {
		t.Fatal(err)
	}
	db3, err := mgr.Get(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if db3 == db1 {
		t.Fatal("expected a fresh DB handle after Remove")
	}

	if err := mgr.CloseAll(); err != nil {
		t.Fatal(err)
	}
}

func TestManagerWithInjectedDB(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()

	mgr := New()
	spec := Spec{
		Name:   "injected-sqlite",
		Driver: DriverSQLite,
		DB:     raw,
	}

	err = mgr.With(context.Background(), spec, func(db *sqlx.DB) error {
		_, err := db.ExecContext(context.Background(), "SELECT 1")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
