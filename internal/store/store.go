package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/piper/piper/internal/store/sqlite"
	"github.com/piper/piper/pkg/logstore"
	"github.com/piper/piper/pkg/run"
	"github.com/piper/piper/pkg/schedule"
	"github.com/piper/piper/pkg/serving"
	"github.com/piper/piper/pkg/worker"
)

// Repos holds all repository implementations for the selected driver.
// Add new drivers by implementing each Repository interface and registering here.
type Repos struct {
	Run      run.Repository
	Step     run.StepRepository
	Schedule schedule.Repository
	Worker   worker.Repository
	Serving  serving.Repository
	Log      logstore.LogStore

	db     *sql.DB
	ownsDB bool
}

// Open opens a SQLite file and returns Repos with all repositories wired.
func Open(path string) (*Repos, error) {
	db, err := sql.Open("sqlite", path+"?_journal=WAL&_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	return newRepos(db, "sqlite", true)
}

// New creates Repos from an externally injected *sql.DB (SQLite assumed).
// Calling Close does not close db — the caller retains ownership.
func New(db *sql.DB) (*Repos, error) {
	return newRepos(db, "sqlite", false)
}

// NewWithDSN creates Repos from a driver + DSN string.
func NewWithDSN(driver, dsn string) (*Repos, error) {
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	return newRepos(db, driver, true)
}

func newRepos(db *sql.DB, driver string, ownsDB bool) (*Repos, error) {
	if err := migrate(db, driver); err != nil {
		if ownsDB {
			_ = db.Close()
		}
		return nil, fmt.Errorf("migrate: %w", err)
	}

	switch driver {
	case "sqlite", "sqlite3", "":
		return &Repos{
			Run:      sqlite.NewRunRepo(db),
			Step:     sqlite.NewStepRepo(db),
			Schedule: sqlite.NewScheduleRepo(db),
			Worker:   sqlite.NewWorkerRepo(db),
			Serving:  sqlite.NewServingRepo(db),
			Log:      logstore.NewSQLite(db),
			db:       db,
			ownsDB:   ownsDB,
		}, nil
	default:
		if ownsDB {
			_ = db.Close()
		}
		return nil, fmt.Errorf("unsupported db driver: %s (only sqlite supported currently)", driver)
	}
}

// Close closes the underlying DB if owned by this Repos.
func (r *Repos) Close() error {
	if r.ownsDB {
		return r.db.Close()
	}
	return nil
}

// DB returns the underlying *sql.DB.
func (r *Repos) DB() *sql.DB {
	return r.db
}

// DeleteRun removes a run and all its steps and logs atomically.
func (r *Repos) DeleteRun(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM logs WHERE run_id=?`,
		`DELETE FROM steps WHERE run_id=?`,
		`DELETE FROM runs WHERE id=?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
