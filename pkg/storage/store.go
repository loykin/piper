package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// Store abstracts all artifact storage backends.
// Keys are slash-separated paths (e.g. "run-abc/step-1/output/model.pt").
type Store interface {
	// Put uploads r to the given key. size -1 means unknown length.
	Put(ctx context.Context, key string, r io.Reader, size int64) error

	// Get returns a reader for the given key. Returns ErrNotFound if absent.
	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// List returns all keys with the given prefix, in arbitrary order.
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)

	// Delete removes one or more keys. Non-existent keys are silently ignored.
	Delete(ctx context.Context, keys ...string) error

	// URL returns the public-accessible URL for the given key.
	// Returns ("", false) when the backend cannot produce a direct URL.
	URL(key string) (string, bool)
}

// ObjectInfo describes a stored object.
type ObjectInfo struct {
	Key        string
	Size       int64
	ModifiedAt time.Time
}

// ErrNotFound is returned by Get when the key does not exist.
var ErrNotFound = errors.New("blobstore: key not found")
