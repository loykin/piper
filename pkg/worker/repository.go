package worker

import (
	"context"
	"time"
)

// Repository is the persistence interface for Worker records.
type Repository interface {
	Upsert(ctx context.Context, w *WorkerRecord) error
	Heartbeat(ctx context.Context, id string, inFlight int) error
	SetStatus(ctx context.Context, id, status string) error
	Get(ctx context.Context, id string) (*WorkerRecord, error)
	List(ctx context.Context, onlineOnly bool) ([]*WorkerRecord, error)
	MarkOffline(ctx context.Context, before time.Time) error
}
