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

	// List returns keys with the given prefix, in arbitrary order.
	//
	// When delimiter is empty, List is fully recursive: every matching key is
	// returned, regardless of depth.
	//
	// When delimiter is non-empty, List follows S3 ListObjectsV2's Delimiter
	// semantics (the same convention used by the AWS/MinIO/GCS/Azure consoles
	// to render a folder tree from a flat key namespace): keys are returned
	// as-is up to and including the first delimiter occurrence after prefix,
	// aggregated into a single ObjectInfo with IsDir=true and Key truncated
	// right after that delimiter. Everything deeper collapses into that one
	// pseudo-directory entry instead of being listed individually — callers
	// drill in by calling List again with that entry's Key as the new prefix.
	List(ctx context.Context, prefix, delimiter string) ([]ObjectInfo, error)

	// Delete removes one or more keys. Non-existent keys are silently ignored.
	Delete(ctx context.Context, keys ...string) error

	// URL returns the public-accessible URL for the given key.
	// Returns ("", false) when the backend cannot produce a direct URL.
	URL(key string) (string, bool)
}

// ObjectInfo describes a stored object, or (when IsDir is true) a
// pseudo-directory aggregated by a delimiter-scoped List call — see List's
// doc comment. Size and ModifiedAt are zero-valued for directory entries.
type ObjectInfo struct {
	Key        string    `json:"key"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
	IsDir      bool      `json:"is_dir"`
}

// ErrNotFound is returned by Get when the key does not exist.
var ErrNotFound = errors.New("blobstore: key not found")
