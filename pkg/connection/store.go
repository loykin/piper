package connection

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
)

type Store struct {
	repo Repository
	aead cipher.AEAD
}

// NewStoreFromKey creates a Store using the same key format as secret.Store:
//   - "sha256:<passphrase>" → SHA-256 of passphrase
//   - base64-encoded 32 bytes
//   - raw 32-byte string
func NewStoreFromKey(repo Repository, key string) (*Store, error) {
	if repo == nil {
		return nil, fmt.Errorf("connection repository is required")
	}
	raw, err := decodeKey(key)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, fmt.Errorf("connection encryption key: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("connection encryption: %w", err)
	}
	return &Store{repo: repo, aead: aead}, nil
}

func (s *Store) List(ctx context.Context, projectID string) ([]*Metadata, error) {
	return s.repo.List(ctx, projectID)
}

func (s *Store) Get(ctx context.Context, projectID, name string) (*Metadata, error) {
	return s.repo.Get(ctx, projectID, name)
}

func (s *Store) Create(ctx context.Context, projectID string, req CreateRequest) (*Metadata, error) {
	meta, value, err := normalizeCreate(projectID, req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	encrypted, err := s.encrypt(value)
	if err != nil {
		return nil, err
	}
	if err := s.repo.Create(ctx, meta, encrypted); err != nil {
		return nil, err
	}
	return meta, nil
}

func (s *Store) Rotate(ctx context.Context, projectID, name string, req RotateRequest) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if len(req.Data) == 0 {
		return fmt.Errorf("%w: data is required", ErrInvalid)
	}
	meta, err := s.repo.Get(ctx, projectID, name)
	if err != nil {
		return err
	}
	if meta == nil {
		return ErrNotFound
	}
	if meta.Disabled {
		return ErrDisabled
	}
	encrypted, err := s.encrypt(Value{Data: cleanData(req.Data)})
	if err != nil {
		return err
	}
	return s.repo.Rotate(ctx, projectID, name, encrypted)
}

func (s *Store) Patch(ctx context.Context, projectID, name string, req PatchRequest) (*Metadata, error) {
	if req.Endpoint != nil {
		meta, err := s.repo.Get(ctx, projectID, name)
		if err != nil {
			return nil, err
		}
		if meta == nil {
			return nil, ErrNotFound
		}
		ep := normalizeEndpoint(*req.Endpoint)
		if err := validateEndpoint(meta.Type, ep); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		req.Endpoint = &ep
	}
	if err := s.repo.Patch(ctx, projectID, name, req); err != nil {
		return nil, err
	}
	meta, err := s.repo.Get(ctx, projectID, name)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, ErrNotFound
	}
	return meta, nil
}

func (s *Store) Delete(ctx context.Context, projectID, name string) error {
	return s.repo.Delete(ctx, projectID, name)
}

// Resolve decrypts and returns the credentials for a connection.
// For git connections, it validates repoURL (if non-empty) is within endpoint scope.
func (s *Store) Resolve(ctx context.Context, projectID, name, repoURL string) (Value, error) {
	meta, err := s.repo.Get(ctx, projectID, name)
	if err != nil {
		return Value{}, err
	}
	if meta == nil {
		return Value{}, ErrNotFound
	}
	if meta.Disabled {
		return Value{}, ErrDisabled
	}
	if repoURL != "" && meta.Endpoint != "" {
		if err := checkScope(meta.Endpoint, repoURL); err != nil {
			return Value{}, err
		}
	}
	encrypted, err := s.repo.GetValue(ctx, projectID, name)
	if err != nil {
		return Value{}, err
	}
	value, err := s.decrypt(encrypted)
	if err != nil {
		return Value{}, err
	}
	_ = s.repo.MarkUsed(ctx, projectID, name)
	return value, nil
}

// GitEnv resolves a git connection and returns PIPER_GIT_TOKEN / PIPER_GIT_USER env vars.
func (s *Store) GitEnv(ctx context.Context, projectID, name, repoURL string) ([]string, error) {
	meta, err := s.repo.Get(ctx, projectID, name)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, ErrNotFound
	}
	if meta.Type != TypeGit {
		return nil, fmt.Errorf("%w: connection %q is type %q, expected git", ErrInvalid, name, meta.Type)
	}
	value, err := s.Resolve(ctx, projectID, name, repoURL)
	if err != nil {
		return nil, err
	}
	token := firstNonEmpty(value.Data["token"], value.Data["password"])
	if token == "" {
		return nil, fmt.Errorf("connection %q missing token", name)
	}
	env := []string{"PIPER_GIT_TOKEN=" + token}
	if user := firstNonEmpty(value.Data["username"], value.Data["user"]); user != "" {
		env = append(env, "PIPER_GIT_USER="+user)
	}
	return env, nil
}

// FindByRepo returns the best-matching git Connection for repoURL
// based on endpoint prefix (longest match wins). Returns nil if none found.
func (s *Store) FindByRepo(ctx context.Context, projectID, repoURL string) (*Metadata, error) {
	all, err := s.repo.List(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var best *Metadata
	for _, m := range all {
		if m.Disabled || m.Type != TypeGit || m.Endpoint == "" {
			continue
		}
		if !inScope(m.Endpoint, repoURL) {
			continue
		}
		if best == nil || len(m.Endpoint) > len(best.Endpoint) {
			best = m
		}
	}
	return best, nil
}

func (s *Store) RecordTestResult(ctx context.Context, projectID, name string, ok bool, message string) error {
	return s.repo.RecordTestResult(ctx, projectID, name, ok, message)
}

// checkScope returns ErrScopeViolation if repoURL is not within endpoint scope.
func checkScope(endpoint, repoURL string) error {
	if !inScope(endpoint, repoURL) {
		return fmt.Errorf("%w: %q not in scope %q", ErrScopeViolation, repoURL, endpoint)
	}
	return nil
}

// inScope returns true when repoURL falls within the endpoint prefix using
// scheme+host exact match and path segment boundary.
// e.g. endpoint "https://github.com/myorg/" matches "https://github.com/myorg/repo"
// but NOT "https://github.com/myorg-evil/repo".
func inScope(endpoint, repoURL string) bool {
	ep, err := url.Parse(endpoint)
	if err != nil || ep.Host == "" {
		return false
	}
	repo, err := url.Parse(repoURL)
	if err != nil {
		return false
	}
	if ep.Scheme != repo.Scheme || ep.Host != repo.Host {
		return false
	}
	epPath := ep.Path
	if !strings.HasSuffix(epPath, "/") {
		epPath += "/"
	}
	repoPath := repo.Path
	if !strings.HasSuffix(repoPath, "/") {
		repoPath += "/"
	}
	return strings.HasPrefix(repoPath, epPath)
}

func normalizeCreate(projectID string, req CreateRequest) (*Metadata, Value, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, Value{}, fmt.Errorf("name is required")
	}
	if req.Type != TypeGit && req.Type != TypeRegistry {
		return nil, Value{}, fmt.Errorf("type must be git or registry")
	}
	if err := validateEndpoint(req.Type, req.Endpoint); err != nil {
		return nil, Value{}, err
	}
	if len(req.Data) == 0 {
		return nil, Value{}, fmt.Errorf("data is required")
	}
	data := cleanData(req.Data)
	if req.Type == TypeGit && firstNonEmpty(data["token"], data["password"]) == "" {
		return nil, Value{}, fmt.Errorf("git connection requires token or password")
	}
	if req.Type == TypeRegistry && firstNonEmpty(data["password"]) == "" {
		return nil, Value{}, fmt.Errorf("registry connection requires password")
	}
	meta := &Metadata{
		ProjectID: projectID,
		Name:      name,
		Type:      req.Type,
		Endpoint:  normalizeEndpoint(req.Endpoint),
	}
	return meta, Value{Data: data}, nil
}

// validateEndpoint checks endpoint format. Empty endpoint is always valid.
func validateEndpoint(t Type, endpoint string) error {
	if endpoint == "" {
		return nil
	}
	if t == TypeGit {
		u, err := url.Parse(endpoint)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("git endpoint must be a valid URL (e.g. https://github.com/myorg/)")
		}
		if !strings.HasSuffix(endpoint, "/") {
			return fmt.Errorf("git endpoint must end with /")
		}
	}
	return nil
}

func normalizeEndpoint(endpoint string) string {
	ep := strings.TrimSpace(endpoint)
	if ep != "" && !strings.HasSuffix(ep, "/") {
		ep += "/"
	}
	return ep
}

func cleanData(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		out[k] = v
	}
	return out
}

func (s *Store) encrypt(value Value) ([]byte, error) {
	plain, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	ciphertext := s.aead.Seal(nil, nonce, plain, nil)
	out := append([]byte("v1:"), nonce...)
	return append(out, ciphertext...), nil
}

func (s *Store) decrypt(encrypted []byte) (Value, error) {
	if len(encrypted) < 3+s.aead.NonceSize() || string(encrypted[:3]) != "v1:" {
		return Value{}, fmt.Errorf("invalid connection ciphertext")
	}
	body := encrypted[3:]
	nonce := body[:s.aead.NonceSize()]
	ciphertext := body[s.aead.NonceSize():]
	plain, err := s.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return Value{}, fmt.Errorf("decrypt connection: %w", err)
	}
	var value Value
	if err := json.Unmarshal(plain, &value); err != nil {
		return Value{}, err
	}
	if value.Data == nil {
		value.Data = map[string]string{}
	}
	return value, nil
}

func decodeKey(key string) ([]byte, error) {
	key = strings.TrimSpace(key)
	if strings.HasPrefix(key, "sha256:") {
		sum := sha256.Sum256([]byte(strings.TrimPrefix(key, "sha256:")))
		return sum[:], nil
	}
	if b, err := base64.StdEncoding.DecodeString(key); err == nil && len(b) == 32 {
		return b, nil
	}
	if len(key) == 32 {
		return []byte(key), nil
	}
	return nil, fmt.Errorf("connection encryption key must be 32 bytes, base64-encoded 32 bytes, or sha256:<passphrase>")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
