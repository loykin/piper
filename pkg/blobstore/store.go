package blobstore

import (
	"context"
	"errors"
	"io"
	"time"
)

// Store abstracts all artifact storage backends.
// Key는 슬래시 구분자를 사용하는 경로 (예: "run-abc/step-1/output/model.pt")
type Store interface {
	// Put uploads r to the given key. size가 -1이면 길이 미상.
	Put(ctx context.Context, key string, r io.Reader, size int64) error

	// Get returns a reader for the given key.
	// 키가 없으면 ErrNotFound 반환.
	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// List returns all keys with the given prefix, in arbitrary order.
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)

	// Delete removes one or more keys. 존재하지 않는 키는 무시.
	Delete(ctx context.Context, keys ...string) error

	// URL returns the public-accessible URL for the given key.
	// 백엔드가 직접 URL을 제공할 수 없는 경우 ("", false) 반환.
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
