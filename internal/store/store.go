package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/loykin/dbstore"
	sqlxadapter "github.com/loykin/dbstore/adapters/sqlx"
	_ "modernc.org/sqlite"

	"github.com/loykin/piper/internal/logstore"
	"github.com/loykin/piper/internal/projectclient"
	"github.com/loykin/piper/internal/store/postgres"
	"github.com/loykin/piper/internal/store/sqlite"
	"github.com/loykin/piper/pkg/alerting"
	"github.com/loykin/piper/pkg/credential"
	"github.com/loykin/piper/pkg/federation"
	"github.com/loykin/piper/pkg/integration/mlflow"
	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/schedule"
	"github.com/loykin/piper/pkg/serving"
	"github.com/loykin/piper/pkg/template"
	"github.com/loykin/piper/pkg/viewer"
)

// Repos holds all repository implementations for the selected driver.
// Add new drivers by implementing each Repository interface and registering here.
type Repos struct {
	Project          project.Repository
	Federation       federation.Repository
	Run              run.Repository
	Submission       run.SubmissionRepository
	ProjectMutation  projectclient.MutationRepository
	Step             run.StepRepository
	Schedule         schedule.Repository
	AlertRule        alerting.Repository
	Credential       credential.Repository
	Viewer           viewer.Repository
	Serving          serving.Repository
	Notebook         notebook.Repository
	NotebookVolume   notebook.VolumeRepository
	PipelineTemplate template.Repository
	Mlflow           mlflow.Repository
	Log              logstore.LogStore
	Metric           logstore.MetricStore

	// db is owned by pool; retained here for DB() and deleteRunQueries rebind.
	// Callers must not use DB() after Close.
	db        *sqlx.DB
	adapter   *sqlxadapter.Adapter
	executor  *dbstore.Executor[*sqlx.DB]
	driver    string
	closeFunc func() error
	deleteRun func(ctx context.Context, projectID, id string) error
}

// ExternalReposConfig is used to build a Repos from externally supplied implementations.
// Use this when embedding piper in an application that already manages its own database.
type ExternalReposConfig struct {
	Project          project.Repository
	Federation       federation.Repository
	Run              run.Repository
	Submission       run.SubmissionRepository
	ProjectMutation  projectclient.MutationRepository
	Step             run.StepRepository
	Schedule         schedule.Repository
	AlertRule        alerting.Repository
	Credential       credential.Repository
	Serving          serving.Repository
	Notebook         notebook.Repository
	NotebookVolume   notebook.VolumeRepository
	PipelineTemplate template.Repository
	Mlflow           mlflow.Repository
	Log              logstore.LogStore
	Metric           logstore.MetricStore
	// DeleteRun handles atomic deletion of a run and all its steps. Stats have
	// an independent lifecycle and must not be removed by this callback.
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

func (d *driverAdapter) Open(cfg dbstore.SourceConfig) (*sqlx.DB, error) {
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

func openDBStore(driver, dsn string, cfg dbstore.PoolConfig) (*sqlx.DB, *sqlxadapter.Adapter, *dbstore.Executor[*sqlx.DB], error) {
	adapter := &driverAdapter{driver: driver, apply: sqlxadapter.ApplyPoolConfig}
	if driver == "sqlite" {
		adapter.apply = applySQLitePoolConfig
	}

	sa := sqlxadapter.New()
	sa.RegisterDriver(driver, adapter)
	if err := sa.Open(PrimarySource, dbstore.SourceConfig{
		Driver:     driver,
		DSN:        dsn,
		PoolConfig: cfg,
	}); err != nil {
		return nil, nil, nil, err
	}
	return adapter.db, sa, sa.Executor(), nil
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

func newReposFromDBStore(db *sqlx.DB, driver string, adapter *sqlxadapter.Adapter, executor *dbstore.Executor[*sqlx.DB]) (*Repos, error) {
	if !supportedDriver(driver) {
		adapter.Close()
		return nil, fmt.Errorf("unsupported db driver: %s", driver)
	}
	if err := migrate(context.Background(), db, driver); err != nil {
		adapter.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return buildRepos(db, driver, adapter, executor), nil
}

func supportedDriver(driver string) bool {
	switch driver {
	case "sqlite", "sqlite3", "", "postgres", "postgresql":
		return true
	default:
		return false
	}
}

func buildRepos(db *sqlx.DB, driver string, adapter *sqlxadapter.Adapter, executor *dbstore.Executor[*sqlx.DB]) *Repos {
	switch driver {
	case "sqlite", "sqlite3", "":
		return &Repos{
			Project:          sqlite.NewProjectRepo(executor, PrimarySource),
			Federation:       sqlite.NewFederationRepo(executor, PrimarySource),
			Run:              sqlite.NewRunRepo(executor, PrimarySource),
			Submission:       sqlite.NewSubmissionRepo(executor, PrimarySource),
			ProjectMutation:  sqlite.NewProjectMutationRepo(executor, PrimarySource),
			Step:             sqlite.NewStepRepo(executor, PrimarySource),
			Schedule:         sqlite.NewScheduleRepo(executor, PrimarySource),
			AlertRule:        sqlite.NewAlertRuleRepo(executor, PrimarySource),
			Credential:       sqlite.NewCredentialRepo(executor, PrimarySource),
			Serving:          sqlite.NewServingRepo(executor, PrimarySource),
			Notebook:         sqlite.NewNotebookRepo(executor, PrimarySource),
			NotebookVolume:   sqlite.NewNotebookVolumeRepo(executor, PrimarySource),
			PipelineTemplate: sqlite.NewPipelineRepo(executor, PrimarySource),
			Mlflow:           sqlite.NewMlflowRepo(executor, PrimarySource, mlflow.DefaultSSRFPolicy()),
			Viewer:           sqlite.NewViewerRepo(executor, PrimarySource),
			Log:              logstore.NewSQLite(executor, PrimarySource),
			Metric:           logstore.NewSQLite(executor, PrimarySource),
			db:               db,
			adapter:          adapter,
			executor:         executor,
			driver:           driver,
		}
	case "postgres", "postgresql":
		return &Repos{
			Project:          postgres.NewProjectRepo(executor, PrimarySource),
			Federation:       postgres.NewFederationRepo(executor, PrimarySource),
			Run:              postgres.NewRunRepo(executor, PrimarySource),
			Submission:       postgres.NewSubmissionRepo(executor, PrimarySource),
			ProjectMutation:  postgres.NewProjectMutationRepo(executor, PrimarySource),
			Step:             postgres.NewStepRepo(executor, PrimarySource),
			Schedule:         postgres.NewScheduleRepo(executor, PrimarySource),
			AlertRule:        postgres.NewAlertRuleRepo(executor, PrimarySource),
			Credential:       postgres.NewCredentialRepo(executor, PrimarySource),
			Serving:          postgres.NewServingRepo(executor, PrimarySource),
			Notebook:         postgres.NewNotebookRepo(executor, PrimarySource),
			NotebookVolume:   postgres.NewNotebookVolumeRepo(executor, PrimarySource),
			PipelineTemplate: postgres.NewPipelineRepo(executor, PrimarySource),
			Mlflow:           postgres.NewMlflowRepo(executor, PrimarySource, mlflow.DefaultSSRFPolicy()),
			Viewer:           postgres.NewViewerRepo(executor, PrimarySource),
			Log:              logstore.NewPostgres(executor, PrimarySource),
			Metric:           logstore.NewPostgres(executor, PrimarySource),
			db:               db,
			adapter:          adapter,
			executor:         executor,
			driver:           driver,
		}
	}
	return &Repos{db: db, adapter: adapter, executor: executor, driver: driver}
}

// Close closes the underlying pool, or calls the custom closer if set.
func (r *Repos) Close() error {
	if r.closeFunc != nil {
		return r.closeFunc()
	}
	if r.adapter != nil {
		r.adapter.Close()
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
func (r *Repos) Executor() *dbstore.Executor[*sqlx.DB] {
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

// DeleteRun removes a run and all its steps atomically. Logs and metrics have
// their own retention lifecycle and deliberately survive run deletion.
func (r *Repos) DeleteRun(ctx context.Context, projectID, id string) error {
	if r.deleteRun != nil {
		return r.deleteRun(ctx, projectID, id)
	}
	if r.executor == nil {
		return fmt.Errorf("DeleteRun: no executor configured — set ExternalReposConfig.DeleteRun")
	}
	return sqlxadapter.RunTx(r.executor, ctx, PrimarySource, func(ctx context.Context, tx *sqlx.Tx) error {
		for _, q := range deleteRunQueries(r.db) {
			if _, err := tx.ExecContext(ctx, q, projectID, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteRuns removes multiple runs and their steps atomically. Stats survive.
func (r *Repos) DeleteRuns(ctx context.Context, projectID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if r.deleteRun != nil {
		for _, id := range ids {
			if err := r.deleteRun(ctx, projectID, id); err != nil {
				return err
			}
		}
		return nil
	}
	if r.executor == nil {
		return fmt.Errorf("DeleteRuns: no executor configured — set ExternalReposConfig.DeleteRun")
	}
	return sqlxadapter.RunTx(r.executor, ctx, PrimarySource, func(ctx context.Context, tx *sqlx.Tx) error {
		for _, spec := range deleteRunsQueries() {
			query, args, err := sqlx.In(spec.query, projectID, ids)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, r.db.Rebind(query), args...); err != nil {
				return err
			}
		}
		return nil
	})
}

func deleteRunQueries(db *sqlx.DB) []string {
	return []string{
		db.Rebind(`DELETE FROM steps WHERE project_id=? AND run_id=?`),
		db.Rebind(`DELETE FROM runs WHERE project_id=? AND id=?`),
	}
}

func deleteRunsQueries() []struct {
	query string
} {
	return []struct {
		query string
	}{
		{`DELETE FROM steps WHERE project_id=? AND run_id IN (?)`},
		{`DELETE FROM runs WHERE project_id=? AND id IN (?)`},
	}
}
