package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"

	"github.com/piper/piper/internal/store/postgres"
	"github.com/piper/piper/internal/store/sqlite"
	"github.com/piper/piper/internal/worker"
	"github.com/piper/piper/pkg/logstore"
	"github.com/piper/piper/pkg/run"
	"github.com/piper/piper/pkg/schedule"
	"github.com/piper/piper/pkg/serving"
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
	Metric   logstore.MetricStore

	db        *sqlx.DB
	driver    string
	ownsDB    bool
	closeFunc func() error
	deleteRun func(ctx context.Context, id string) error
}

// ExternalReposConfig is used to build a Repos from externally supplied implementations.
// Use this when embedding piper in an application that already manages its own database.
type ExternalReposConfig struct {
	Run      run.Repository
	Step     run.StepRepository
	Schedule schedule.Repository
	Worker   worker.Repository
	Serving  serving.Repository
	Log      logstore.LogStore
	Metric   logstore.MetricStore
	// DeleteRun handles atomic deletion of a run with all its steps, logs, and metrics.
	// If nil, DeleteRun returns an error — provide an implementation for the target database.
	DeleteRun func(ctx context.Context, id string) error
	// Close is called when Repos.Close() is invoked. May be nil.
	Close func() error
}

// Open opens a SQLite file and returns Repos with all repositories wired.
func Open(path string) (*Repos, error) {
	db, err := sqlx.Open("sqlite", path+"?_journal=WAL&_timeout=5000")
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
	return newRepos(sqlx.NewDb(db, "sqlite"), "sqlite", false)
}

// OpenPostgres opens a PostgreSQL connection and returns Repos with all repositories wired.
func OpenPostgres(dsn string) (*Repos, error) {
	db, err := sqlx.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	return newRepos(db, "postgres", true)
}

func newRepos(db *sqlx.DB, driver string, ownsDB bool) (*Repos, error) {
	if err := migrate(context.Background(), db, driver); err != nil {
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
			Log:      logstore.NewSQLite(db.DB),
			Metric:   logstore.NewSQLite(db.DB),
			db:       db,
			driver:   driver,
			ownsDB:   ownsDB,
		}, nil
	case "postgres", "postgresql":
		return &Repos{
			Run:      postgres.NewRunRepo(db),
			Step:     postgres.NewStepRepo(db),
			Schedule: postgres.NewScheduleRepo(db),
			Worker:   postgres.NewWorkerRepo(db),
			Serving:  postgres.NewServingRepo(db),
			Log:      logstore.NewPostgres(db.DB),
			Metric:   logstore.NewPostgres(db.DB),
			db:       db,
			driver:   driver,
			ownsDB:   ownsDB,
		}, nil

	default:
		if ownsDB {
			_ = db.Close()
		}
		return nil, fmt.Errorf("unsupported db driver: %s", driver)
	}
}

// Close closes the underlying DB if owned, or calls the custom closer if set.
func (r *Repos) Close() error {
	if r.closeFunc != nil {
		return r.closeFunc()
	}
	if r.ownsDB && r.db != nil {
		return r.db.Close()
	}
	return nil
}

// DB returns the underlying *sql.DB, or nil for externally-supplied repos.
func (r *Repos) DB() *sql.DB {
	if r.db == nil {
		return nil
	}
	return r.db.DB
}

// DeleteRun removes a run and all its steps, logs, and metrics atomically.
func (r *Repos) DeleteRun(ctx context.Context, id string) error {
	if r.deleteRun != nil {
		return r.deleteRun(ctx, id)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, q := range []string{
		r.db.Rebind(`DELETE FROM run_metrics WHERE run_id=?`),
		r.db.Rebind(`DELETE FROM logs WHERE run_id=?`),
		r.db.Rebind(`DELETE FROM steps WHERE run_id=?`),
		r.db.Rebind(`DELETE FROM runs WHERE id=?`),
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
