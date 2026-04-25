package worker

import "time"

const (
	StatusOnline = "online"
)

// WorkerRecord is the DB/persistence model for a worker node.
type WorkerRecord struct {
	ID           string    `json:"id"            db:"id"`
	Label        string    `json:"label"         db:"label"`
	Hostname     string    `json:"hostname"      db:"hostname"`
	Concurrency  int       `json:"concurrency"   db:"concurrency"`
	Status       string    `json:"status"        db:"status"`
	InFlight     int       `json:"in_flight"     db:"in_flight"`
	RegisteredAt time.Time `json:"registered_at" db:"registered_at"`
	LastSeenAt   time.Time `json:"last_seen_at"  db:"last_seen_at"`
}

// Info is the in-memory representation used by the registry (also returned by the API).
type Info struct {
	ID           string    `json:"id"`
	Label        string    `json:"label"`
	Concurrency  int       `json:"concurrency"`
	Hostname     string    `json:"hostname"`
	RegisteredAt time.Time `json:"registered_at"`
	LastSeen     time.Time `json:"last_seen"`
	Status       string    `json:"status"`
	InFlight     int       `json:"in_flight"`
}
