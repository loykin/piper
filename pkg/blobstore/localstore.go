package blobstore

import (
	"context"
	"io"
	"os"
	"path/filepath"
)

// LocalStore implements Store using the local filesystem.
// 단일 머신 개발·테스트 및 NFS 마운트 공유 볼륨에 사용합니다.
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

func (s *LocalStore) fullPath(key string) string {
	return filepath.Join(s.root, filepath.FromSlash(key))
}

func (s *LocalStore) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	p := s.fullPath(key)
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
	f, err := os.Open(s.fullPath(key))
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	return f, err
}

func (s *LocalStore) List(_ context.Context, prefix string) ([]ObjectInfo, error) {
	searchRoot := filepath.Join(s.root, filepath.FromSlash(prefix))
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

func (s *LocalStore) Delete(_ context.Context, keys ...string) error {
	for _, key := range keys {
		if err := os.Remove(s.fullPath(key)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (s *LocalStore) URL(_ string) (string, bool) { return "", false }

// Root returns the absolute root directory of this store.
func (s *LocalStore) Root() string { return s.root }
