package blobstore

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"time"
)

// MemStore is an in-memory Store implementation for testing.
type MemStore struct {
	mu   sync.RWMutex
	data map[string]memObj
}

type memObj struct {
	data       []byte
	modifiedAt time.Time
}

// NewMemStore creates an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{data: make(map[string]memObj)}
}

func (s *MemStore) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.data[key] = memObj{data: b, modifiedAt: time.Now().UTC()}
	s.mu.Unlock()
	return nil
}

func (s *MemStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.RLock()
	obj, ok := s.data[key]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(obj.data)), nil
}

func (s *MemStore) List(_ context.Context, prefix string) ([]ObjectInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []ObjectInfo
	for k, obj := range s.data {
		if strings.HasPrefix(k, prefix) {
			result = append(result, ObjectInfo{
				Key:        k,
				Size:       int64(len(obj.data)),
				ModifiedAt: obj.modifiedAt,
			})
		}
	}
	return result, nil
}

func (s *MemStore) Delete(_ context.Context, keys ...string) error {
	s.mu.Lock()
	for _, k := range keys {
		delete(s.data, k)
	}
	s.mu.Unlock()
	return nil
}

func (s *MemStore) URL(_ string) (string, bool) { return "", false }

// Keys returns all currently stored keys (for testing/inspection).
func (s *MemStore) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	return keys
}
