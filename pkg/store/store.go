package store

import (
	"database/sql"
	"fmt"

	"github.com/piper/piper/pkg/logstore"
	_ "modernc.org/sqlite"
)

type Store struct {
	db     *sql.DB
	ownsDB bool // true means db is closed when Close is called
}

// Open opens a sqlite file and creates a Store (piper manages the DB directly).
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_journal=WAL&_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	return newStore(db, true)
}

// New creates a Store from an externally injected *sql.DB.
// Use this when a DB connection already exists (e.g. data-voyager).
// Calling Close does not close db — the caller retains ownership.
//
//	db, _ := sql.Open("sqlite", "./app.db")
//	store, _ := store.New(db)
func New(db *sql.DB) (*Store, error) {
	return newStore(db, false)
}

// NewWithDSN creates a Store from a DSN string.
// driver is the database/sql driver name, e.g. "sqlite" or "postgres".
//
//	store, _ := store.NewWithDSN("sqlite", "./piper.db")
//	store, _ := store.NewWithDSN("postgres", "host=localhost dbname=voyager")
func NewWithDSN(driver, dsn string) (*Store, error) {
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	return newStore(db, true)
}

func newStore(db *sql.DB, ownsDB bool) (*Store, error) {
	s := &Store{db: db, ownsDB: ownsDB}
	if err := migrate(db); err != nil {
		if ownsDB {
			_ = db.Close()
		}
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error {
	if s.ownsDB {
		return s.db.Close()
	}
	return nil
}

// DB returns the underlying *sql.DB.
// Useful when library users need advanced features such as transactions.
func (s *Store) DB() *sql.DB {
	return s.db
}

// LogStore returns a logstore.LogStore backed by this Store's SQLite database.
func (s *Store) LogStore() *logstore.SQLiteLogStore {
	return logstore.NewSQLite(s.db)
}
