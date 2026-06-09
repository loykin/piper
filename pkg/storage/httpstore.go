package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// HTTPStore implements Store via HTTP PUT/GET against a remote file server.
// Compatible with piper's built-in /store/* routes.
//
// PUT    {baseURL}/{key}              — upload
// GET    {baseURL}/{key}              — download
// DELETE {baseURL}/{key}             — delete
// GET    {baseURL}/?prefix=&list=1   — list keys (JSON: []string)
type HTTPStore struct {
	baseURL string // no trailing slash
	client  *http.Client
	token   string
}

// NewHTTPStore creates an HTTPStore targeting the given base URL.
// token is an optional Bearer token for Authorization headers.
func NewHTTPStore(baseURL, token string) *HTTPStore {
	return &HTTPStore{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{},
		token:   token,
	}
}

func (s *HTTPStore) keyURL(key string) string {
	return s.baseURL + "/" + strings.TrimLeft(key, "/")
}

func (s *HTTPStore) addAuth(req *http.Request) {
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
}

func (s *HTTPStore) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.keyURL(key), r)
	if err != nil {
		return err
	}
	if size >= 0 {
		req.ContentLength = size
	}
	s.addAuth(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("httpstore put %q: status %d", key, resp.StatusCode)
	}
	return nil
}

func (s *HTTPStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.keyURL(key), nil)
	if err != nil {
		return nil, err
	}
	s.addAuth(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return nil, ErrNotFound
	}
	if resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("httpstore get %q: status %d", key, resp.StatusCode)
	}
	return resp.Body, nil
}

func (s *HTTPStore) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	listURL := s.baseURL + "/?prefix=" + url.QueryEscape(prefix) + "&list=1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, err
	}
	s.addAuth(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("httpstore list: status %d", resp.StatusCode)
	}
	var keys []string
	if err := json.NewDecoder(resp.Body).Decode(&keys); err != nil {
		return nil, err
	}
	result := make([]ObjectInfo, len(keys))
	for i, k := range keys {
		result[i] = ObjectInfo{Key: k}
	}
	return result, nil
}

func (s *HTTPStore) Delete(ctx context.Context, keys ...string) error {
	for _, key := range keys {
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.keyURL(key), nil)
		if err != nil {
			return err
		}
		s.addAuth(req)
		resp, err := s.client.Do(req)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
			return fmt.Errorf("httpstore delete %q: status %d", key, resp.StatusCode)
		}
	}
	return nil
}

// URL returns the public-accessible URL for the given key.
// HTTPStore can always produce a direct URL since it uses an HTTP base.
func (s *HTTPStore) URL(key string) (string, bool) {
	return s.keyURL(key), true
}
