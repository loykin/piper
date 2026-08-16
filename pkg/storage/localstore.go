package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LocalStore implements Store using the local filesystem.
// Suitable for single-machine development, testing, and NFS-mounted shared volumes.
type LocalStore struct {
	root string // absolute path
}

// NewLocal creates a LocalStore rooted at the given directory.
// The directory is created if it does not exist.
func NewLocal(root string) (*LocalStore, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0755); err != nil {
		return nil, err
	}
	return &LocalStore{root: abs}, nil
}

// fullPath resolves key to an absolute path and rejects any key that would
// escape s.root (e.g. via ".." segments) — key generally comes from run IDs
// and artifact names that ultimately trace back to HTTP request input.
func (s *LocalStore) fullPath(key string) (string, error) {
	p := filepath.Join(s.root, filepath.FromSlash(key))
	if p != s.root && !strings.HasPrefix(p, s.root+string(os.PathSeparator)) {
		return "", fmt.Errorf("storage: key %q escapes store root", key)
	}
	return p, nil
}

func (s *LocalStore) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	p, err := s.fullPath(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(f, r)
	return err
}

func (s *LocalStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := s.fullPath(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	return f, err
}

func (s *LocalStore) List(_ context.Context, prefix, delimiter string) ([]ObjectInfo, error) {
	searchRoot, err := s.fullPath(prefix)
	if err != nil {
		return nil, err
	}
	if delimiter == "" {
		return s.listRecursive(searchRoot)
	}
	return s.listOneLevel(searchRoot, prefix, delimiter)
}

func (s *LocalStore) listRecursive(searchRoot string) ([]ObjectInfo, error) {
	var result []ObjectInfo
	err := filepath.Walk(searchRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(s.root, path)
		result = append(result, ObjectInfo{
			Key:        filepath.ToSlash(rel),
			Size:       info.Size(),
			ModifiedAt: info.ModTime().UTC(),
		})
		return nil
	})
	return result, err
}

// listOneLevel lists only the immediate children of searchRoot — the
// filesystem already IS a real tree here, so a delimiter-scoped listing is
// just os.ReadDir instead of a recursive Walk. Subdirectories become IsDir
// entries named prefix+name+delimiter, matching S3 Delimiter semantics so
// callers can treat every backend identically.
func (s *LocalStore) listOneLevel(searchRoot, prefix, delimiter string) ([]ObjectInfo, error) {
	entries, err := os.ReadDir(searchRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	keyPrefix := prefix
	if keyPrefix != "" && !strings.HasSuffix(keyPrefix, delimiter) {
		keyPrefix += delimiter
	}
	var result []ObjectInfo
	for _, entry := range entries {
		if entry.IsDir() {
			result = append(result, ObjectInfo{Key: keyPrefix + entry.Name() + delimiter, IsDir: true})
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		result = append(result, ObjectInfo{
			Key:        keyPrefix + entry.Name(),
			Size:       info.Size(),
			ModifiedAt: info.ModTime().UTC(),
		})
	}
	return result, nil
}

func (s *LocalStore) Delete(_ context.Context, keys ...string) error {
	for _, key := range keys {
		p, err := s.fullPath(key)
		if err != nil {
			return err
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (s *LocalStore) URL(_ string) (string, bool) { return "", false }

// Root returns the absolute root directory of this store.
func (s *LocalStore) Root() string { return s.root }
