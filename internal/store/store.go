package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/loykin/dbstore"
	_ "modernc.org/sqlite"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/logstore"
	"github.com/piper/piper/internal/store/postgres"
	"github.com/piper/piper/internal/store/sqlite"
	"github.com/piper/piper/pkg/notebook"
	"github.com/piper/piper/pkg/pipeline/run"
	"github.com/piper/piper/pkg/project"
	"github.com/piper/piper/pkg/schedule"
	"github.com/piper/piper/pkg/serving"
	"github.com/piper/piper/pkg/template"
	"github.com/piper/piper/pkg/viewer"
)

// Repos holds all repository implementations for the selected driver.
// Add new drivers by implementing each Repository interface and registering here.
type Repos struct {
	Project          project.Repository
	Run              run.Repository
	Step             run.StepRepository
	Schedule         schedule.Repository
	Viewer           viewer.Repository
	Serving          serving.Repository
	Notebook         notebook.Repository
	NotebookVolume   notebook.VolumeRepository
	PipelineTemplate template.Repository
	WorkerPodPolicy  iagent.WorkerPodPolicyRepository
	Log              logstore.LogStore
	Metric           logstore.MetricStore

	// db is owned by pool; retained here for DB() and deleteRunQueries rebind.
	// Callers must not use DB() after Close.
	db        *sqlx.DB
	pool      *dbstore.Pool
	executor  *dbstore.Executor
	driver    string
	closeFunc func() error
	deleteRun func(ctx context.Context, projectID, id string) error
}

// ExternalReposConfig is used to build a Repos from externally supplied implementations.
// Use this when embedding piper in an application that already manages its own database.
type ExternalReposConfig struct {
	Project          project.Repository
	Run              run.Repository
	Step             run.StepRepository
	Schedule         schedule.Repository
	Serving          serving.Repository
	Notebook         notebook.Repository
	NotebookVolume   notebook.VolumeRepository
	PipelineTemplate template.Repository
	Log              logstore.LogStore
	Metric           logstore.MetricStore
	// DeleteRun handles atomic deletion of a run with all its steps, logs, and metrics.
	// If nil, DeleteRun returns an error — provide an implementation for the target database.
	DeleteRun func(ctx context.Context, projectID, id string) error
	// Close is called when Repos.Close() is invoked. May be nil.
	Close func() error
}

// Open opens a SQLite file and returns Repos with all repositories wired.
func Open(path string) (*Repos, error) {
	db, pool, executor, err := openDBStore("sqlite", path+"?_journal=WAL&_timeout=5000", sqlitePoolConfig())
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	return newReposFromDBStore(db, "sqlite", pool, executor)
}

// OpenPostgres opens a PostgreSQL connection and returns Repos with all repositories wired.
func OpenPostgres(dsn string) (*Repos, error) {
	db, pool, executor, err := openDBStore("postgres", dsn, postgresPoolConfig())
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	return newReposFromDBStore(db, "postgres", pool, executor)
}

// PrimarySource is the datasource name registered in the pool.
const PrimarySource = "primary"

type driverAdapter struct {
	driver string
	db     *sqlx.DB
	apply  func(*sqlx.DB, dbstore.PoolConfig)
}

func (d *driverAdapter) Open(cfg dbstore.DriverConfig) (*sqlx.DB, error) {
	db, err := sqlx.Connect(d.driver, cfg.DSN)
	if err != nil {
		return nil, err
	}
	d.db = db
	return db, nil
}

func (d *driverAdapter) ApplyPoolConfig(db *sqlx.DB, cfg dbstore.PoolConfig) {
	d.apply(db, cfg)
}

func openDBStore(driver, dsn string, cfg dbstore.PoolConfig) (*sqlx.DB, *dbstore.Pool, *dbstore.Executor, error) {
	adapter := &driverAdapter{driver: driver, apply: dbstore.DefaultApplyPoolConfig}
	if driver == "sqlite" {
		adapter.apply = applySQLitePoolConfig
	}

	registry := dbstore.NewDriverRegistry()
	registry.Register(driver, adapter)
	pool := dbstore.NewPool(registry)
	if err := pool.Register(PrimarySource, dbstore.DriverConfig{
		Driver:     driver,
		DSN:        dsn,
		PoolConfig: cfg,
	}); err != nil {
		return nil, nil, nil, err
	}
	return adapter.db, pool, dbstore.NewExecutor(pool), nil
}

func sqlitePoolConfig() dbstore.PoolConfig {
	return dbstore.PoolConfig{
		MaxOpenConns:   1,
		MaxIdleConns:   1,
		MaxLifetime:    0,
		MaxIdleTime:    5 * time.Minute,
		MaxConcurrency: 1,
	}
}

func postgresPoolConfig() dbstore.PoolConfig {
	return dbstore.PoolConfig{
		MaxOpenConns:   25,
		MaxIdleConns:   5,
		MaxLifetime:    5 * time.Minute,
		MaxIdleTime:    5 * time.Minute,
		MaxConcurrency: 25,
	}
}

func applySQLitePoolConfig(db *sqlx.DB, cfg dbstore.PoolConfig) {
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(cfg.MaxIdleTime)
	db.SetConnMaxLifetime(cfg.MaxLifetime)
}

func newReposFromDBStore(db *sqlx.DB, driver string, pool *dbstore.Pool, executor *dbstore.Executor) (*Repos, error) {
	if !supportedDriver(driver) {
		pool.RemoveAll()
		return nil, fmt.Errorf("unsupported db driver: %s", driver)
	}
	if err := migrate(context.Background(), db, driver); err != nil {
		pool.RemoveAll()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return buildRepos(db, driver, pool, executor), nil
}

func supportedDriver(driver string) bool {
	switch driver {
	case "sqlite", "sqlite3", "", "postgres", "postgresql":
		return true
	default:
		return false
	}
}

func buildRepos(db *sqlx.DB, driver string, pool *dbstore.Pool, executor *dbstore.Executor) *Repos {
	switch driver {
	case "sqlite", "sqlite3", "":
		return &Repos{
			Project:          sqlite.NewProjectRepo(executor, PrimarySource),
			Run:              sqlite.NewRunRepo(executor, PrimarySource),
			Step:             sqlite.NewStepRepo(executor, PrimarySource),
			Schedule:         sqlite.NewScheduleRepo(executor, PrimarySource),
			Serving:          sqlite.NewServingRepo(executor, PrimarySource),
			Notebook:         sqlite.NewNotebookRepo(executor, PrimarySource),
			NotebookVolume:   sqlite.NewNotebookVolumeRepo(executor, PrimarySource),
			PipelineTemplate: sqlite.NewPipelineRepo(executor, PrimarySource),
			Viewer:           sqlite.NewViewerRepo(executor, PrimarySource),
			WorkerPodPolicy:  sqlite.NewWorkerPodPolicyRepo(executor, PrimarySource),
			Log:              logstore.NewSQLite(executor, PrimarySource),
			Metric:           logstore.NewSQLite(executor, PrimarySource),
			db:               db,
			pool:             pool,
			executor:         executor,
			driver:           driver,
		}
	case "postgres", "postgresql":
		return &Repos{
			Project:          postgres.NewProjectRepo(executor, PrimarySource),
			Run:              postgres.NewRunRepo(executor, PrimarySource),
			Step:             postgres.NewStepRepo(executor, PrimarySource),
			Schedule:         postgres.NewScheduleRepo(executor, PrimarySource),
			Serving:          postgres.NewServingRepo(executor, PrimarySource),
			Notebook:         postgres.NewNotebookRepo(executor, PrimarySource),
			NotebookVolume:   postgres.NewNotebookVolumeRepo(executor, PrimarySource),
			PipelineTemplate: postgres.NewPipelineRepo(executor, PrimarySource),
			Viewer:           postgres.NewViewerRepo(executor, PrimarySource),
			WorkerPodPolicy:  postgres.NewWorkerPodPolicyRepo(executor, PrimarySource),
			Log:              logstore.NewPostgres(executor, PrimarySource),
			Metric:           logstore.NewPostgres(executor, PrimarySource),
			db:               db,
			pool:             pool,
			executor:         executor,
			driver:           driver,
		}
	}
	return &Repos{db: db, pool: pool, executor: executor, driver: driver}
}

// Close closes the underlying pool, or calls the custom closer if set.
func (r *Repos) Close() error {
	if r.closeFunc != nil {
		return r.closeFunc()
	}
	if r.pool != nil {
		r.pool.RemoveAll()
		return nil
	}
	return nil
}

// DB returns the underlying *sql.DB. Retained for external auth factory integrations.
// Must not be used after Close.
func (r *Repos) DB() *sql.DB {
	if r.db == nil {
		return nil
	}
	return r.db.DB
}

// Executor returns the dbstore Executor for constructing additional repositories
// (e.g. auth repos) that share the same pool and throttle policy.
func (r *Repos) Executor() *dbstore.Executor {
	return r.executor
}

// Driver returns the normalized database driver used by these repositories.
func (r *Repos) Driver() string {
	switch r.driver {
	case "", "sqlite3":
		return "sqlite"
	case "postgresql":
		return "postgres"
	default:
		return r.driver
	}
}

// DeleteRun removes a run and all its steps, logs, and metrics atomically.
func (r *Repos) DeleteRun(ctx context.Context, projectID, id string) error {
	if r.deleteRun != nil {
		return r.deleteRun(ctx, projectID, id)
	}
	if r.executor == nil {
		return fmt.Errorf("DeleteRun: no executor configured — set ExternalReposConfig.DeleteRun")
	}
	return r.executor.RunTx(ctx, PrimarySource, func(ctx context.Context, tx *sqlx.Tx) error {
		for _, q := range deleteRunQueries(r.db) {
			if _, err := tx.ExecContext(ctx, q, projectID, id); err != nil {
				return err
			}
		}
		return nil
	})
}

func deleteRunQueries(db *sqlx.DB) []string {
	return []string{
		db.Rebind(`DELETE FROM run_metrics WHERE project_id=? AND run_id=?`),
		db.Rebind(`DELETE FROM logs WHERE project_id=? AND run_id=?`),
		db.Rebind(`DELETE FROM steps WHERE project_id=? AND run_id=?`),
		db.Rebind(`DELETE FROM runs WHERE project_id=? AND id=?`),
	}
}
