package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type Store struct {
	db      *sql.DB
	ownsDB  bool // true면 Close 시 db도 닫음
}

// Open은 sqlite 파일을 열고 Store를 생성한다 (piper가 DB를 직접 관리)
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_journal=WAL&_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	return newStore(db, true)
}

// New는 외부에서 주입된 *sql.DB로 Store를 생성한다.
// data-voyager처럼 이미 DB 연결이 있을 때 사용.
// Close를 호출해도 db는 닫히지 않음 (호출자가 관리).
//
//	db, _ := sql.Open("sqlite", "./app.db")
//	store, _ := store.New(db)
func New(db *sql.DB) (*Store, error) {
	return newStore(db, false)
}

// NewWithDSN은 DSN 문자열로 Store를 생성한다.
// driver는 "sqlite", "postgres" 등 database/sql 드라이버 이름.
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
			db.Close()
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

// DB는 내부 *sql.DB를 반환한다.
// 라이브러리 사용자가 트랜잭션 등 고급 기능이 필요할 때 사용.
func (s *Store) DB() *sql.DB {
	return s.db
}
