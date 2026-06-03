package dbmanager

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

const (
	DriverSQLite   = "sqlite"
	DriverPostgres = "postgres"
)

// PoolConfig controls the SQL pool defaults for a managed handle.
type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// Spec describes one managed SQL handle.
//
// If DB is nil, the manager opens and owns a new handle using Driver + DSN.
// If DB is non-nil, the manager wraps the provided DB and ownership stays with
// the caller unless OwnsDB is set to true.
type Spec struct {
	// Name is the cache key for injected DBs or an explicit override for open DSNs.
	// When empty, the manager derives a key from Driver + DSN.
	Name string

	Driver string
	DSN    string

	// DB allows external injection of an already-open database handle.
	DB *sql.DB

	// OwnsDB forces the manager to close the wrapped DB on Remove/CloseAll.
	OwnsDB bool

	Pool PoolConfig
}

// Provider is the minimal callback-based abstraction.
type Provider interface {
	With(ctx context.Context, spec Spec, fn func(*sqlx.DB) error) error
}

// ConnectionManager caches SQL handles and manages their lifecycle.
type ConnectionManager interface {
	Provider
	Get(ctx context.Context, spec Spec) (*sqlx.DB, error)
	Remove(spec Spec) error
	CloseAll() error
	Stats(spec Spec) (sql.DBStats, bool)
}

type Manager struct {
	mu   sync.Mutex
	dbs  map[string]*entry
	owns map[string]bool
}

type entry struct {
	db      *sqlx.DB
	driver  string
	ownsDB  bool
	closeFn func() error
}

// New creates an empty manager.
func New() *Manager {
	return &Manager{
		dbs:  make(map[string]*entry),
		owns: make(map[string]bool),
	}
}

// With resolves a DB handle, runs fn, and keeps lifecycle ownership in the manager.
func (m *Manager) With(ctx context.Context, spec Spec, fn func(*sqlx.DB) error) error {
	db, err := m.Get(ctx, spec)
	if err != nil {
		return err
	}
	return fn(db)
}

// Get returns a cached DB handle or opens one on demand.
func (m *Manager) Get(_ context.Context, spec Spec) (*sqlx.DB, error) {
	key, err := spec.cacheKey()
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	if db, ok := m.dbs[key]; ok {
		m.mu.Unlock()
		return db.db, nil
	}
	m.mu.Unlock()

	db, owns, err := openSpec(spec)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.dbs[key]; ok {
		_ = db.Close()
		return existing.db, nil
	}
	m.dbs[key] = &entry{db: db, driver: spec.driver(), ownsDB: owns}
	m.owns[key] = owns
	return db, nil
}

// Remove closes and forgets the handle at the given key.
func (m *Manager) Remove(spec Spec) error {
	key, err := spec.cacheKey()
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	ent, ok := m.dbs[key]
	if !ok {
		return nil
	}
	delete(m.dbs, key)
	delete(m.owns, key)
	if ent.ownsDB {
		return ent.db.Close()
	}
	return nil
}

// CloseAll closes every owned handle and forgets all cached entries.
func (m *Manager) CloseAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []string
	for key, ent := range m.dbs {
		if ent.ownsDB {
			if err := ent.db.Close(); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", key, err))
			}
		}
		delete(m.dbs, key)
		delete(m.owns, key)
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Stats returns database/sql pool stats for a cached handle.
func (m *Manager) Stats(spec Spec) (sql.DBStats, bool) {
	key, err := spec.cacheKey()
	if err != nil {
		return sql.DBStats{}, false
	}

	m.mu.Lock()
	ent, ok := m.dbs[key]
	m.mu.Unlock()
	if !ok {
		return sql.DBStats{}, false
	}
	return ent.db.DB.Stats(), true
}

func (s Spec) cacheKey() (string, error) {
	if s.Name != "" {
		return s.Name, nil
	}
	driver := s.driver()
	if s.DB != nil {
		return "", fmt.Errorf("dbmanager: name is required when DB is injected")
	}
	if driver == "" {
		return "", fmt.Errorf("dbmanager: driver is required")
	}
	if s.DSN == "" {
		return "", fmt.Errorf("dbmanager: dsn is required")
	}
	return driver + ":" + s.DSN, nil
}

func (s Spec) driver() string {
	return normalizeDriver(s.Driver)
}

func normalizeDriver(driver string) string {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", "sqlite", "sqlite3":
		return DriverSQLite
	case "postgres", "postgresql":
		return DriverPostgres
	default:
		return strings.ToLower(strings.TrimSpace(driver))
	}
}

func openSpec(spec Spec) (*sqlx.DB, bool, error) {
	driver := spec.driver()
	if spec.DB != nil {
		db := sqlx.NewDb(spec.DB, driver)
		return db, spec.OwnsDB, nil
	}

	switch driver {
	case DriverSQLite:
		db, err := sqlx.Open("sqlite", spec.DSN)
		if err != nil {
			return nil, false, err
		}
		configureSQLiteDB(db, spec.Pool)
		return db, true, nil
	case DriverPostgres:
		db, err := sqlx.Open("postgres", spec.DSN)
		if err != nil {
			return nil, false, err
		}
		configurePostgresDB(db, spec.Pool)
		return db, true, nil
	default:
		return nil, false, fmt.Errorf("dbmanager: unsupported driver %q", spec.Driver)
	}
}

func configureSQLiteDB(db *sqlx.DB, pool PoolConfig) {
	// SQLite is sensitive to concurrent writers. Keep the pool conservative so
	// the database/sql pool does not create avoidable lock contention.
	if pool.MaxOpenConns <= 0 {
		pool.MaxOpenConns = 1
	}
	if pool.MaxIdleConns <= 0 {
		pool.MaxIdleConns = 1
	}
	db.SetMaxOpenConns(pool.MaxOpenConns)
	db.SetMaxIdleConns(pool.MaxIdleConns)
	db.SetConnMaxLifetime(pool.ConnMaxLifetime)
}

func configurePostgresDB(db *sqlx.DB, pool PoolConfig) {
	if pool.MaxOpenConns <= 0 {
		pool.MaxOpenConns = 25
	}
	if pool.MaxIdleConns <= 0 {
		pool.MaxIdleConns = 5
	}
	if pool.ConnMaxLifetime <= 0 {
		pool.ConnMaxLifetime = 5 * time.Minute
	}
	db.SetMaxOpenConns(pool.MaxOpenConns)
	db.SetMaxIdleConns(pool.MaxIdleConns)
	db.SetConnMaxLifetime(pool.ConnMaxLifetime)
}
