package credential

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
	"sort"
	"strings"

	"github.com/piper/piper/pkg/manifest"
)

type Store struct {
	repo Repository
	aead cipher.AEAD
}

func NewStore(repo Repository, key string) (*Store, error) {
	if repo == nil {
		return nil, fmt.Errorf("credential repository is required")
	}
	raw, err := decodeKey(key)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, fmt.Errorf("credential encryption key: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("credential encryption: %w", err)
	}
	return &Store{repo: repo, aead: aead}, nil
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
	return nil, fmt.Errorf("credential encryption key must be 32 bytes, base64-encoded 32 bytes, or sha256:<passphrase>")
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
	data := cleanData(req.Data)
	if err := validateData(meta.Kind, data); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	encrypted, err := s.encrypt(Value{Data: data})
	if err != nil {
		return err
	}
	return s.repo.Rotate(ctx, projectID, name, encrypted, keysForKind(meta.Kind, data))
}

func (s *Store) Patch(ctx context.Context, projectID, name string, req PatchRequest) (*Metadata, error) {
	meta, err := s.repo.Get(ctx, projectID, name)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, ErrNotFound
	}
	if req.Endpoint != nil {
		ep := normalizeEndpoint(*req.Endpoint)
		if err := validateEndpoint(meta.Kind, ep); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		req.Endpoint = &ep
	}
	if err := s.repo.Patch(ctx, projectID, name, req); err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, projectID, name)
}

func (s *Store) Delete(ctx context.Context, projectID, name string) error {
	return s.repo.Delete(ctx, projectID, name)
}

func (s *Store) Resolve(ctx context.Context, projectID, name string) (Value, error) {
	return s.resolve(ctx, projectID, name, "", "")
}

func (s *Store) ResolveGit(ctx context.Context, projectID, name, repoURL string) (Value, error) {
	return s.resolve(ctx, projectID, name, string(KindGit), repoURL)
}

func (s *Store) resolve(ctx context.Context, projectID, name, expectedKind, repoURL string) (Value, error) {
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
	if expectedKind != "" && string(meta.Kind) != expectedKind {
		return Value{}, fmt.Errorf("%w: credential %q is kind %q, expected %s", ErrInvalid, name, meta.Kind, expectedKind)
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

func (s *Store) ResolveEnv(ctx context.Context, projectID string, env []manifest.EnvVar) ([]string, error) {
	out := make([]string, 0, len(env))
	for _, e := range env {
		if e.ValueFrom == nil || e.ValueFrom.CredentialRef == nil {
			if e.Value != "" {
				out = append(out, e.Name+"="+e.Value)
			}
			continue
		}
		ref := e.ValueFrom.CredentialRef
		value, err := s.resolve(ctx, projectID, ref.Name, string(KindGeneric), "")
		if err != nil {
			return nil, fmt.Errorf("env %q: resolve credential %q: %w", e.Name, ref.Name, err)
		}
		v, ok := value.Data[ref.Key]
		if !ok {
			return nil, fmt.Errorf("env %q: credential %q has no key %q", e.Name, ref.Name, ref.Key)
		}
		out = append(out, e.Name+"="+v)
	}
	return out, nil
}

func (s *Store) GitEnv(ctx context.Context, projectID, name, repoURL string) ([]string, error) {
	value, err := s.ResolveGit(ctx, projectID, name, repoURL)
	if err != nil {
		return nil, err
	}
	token := firstNonEmpty(value.Data["token"], value.Data["password"])
	if token == "" {
		return nil, fmt.Errorf("credential %q missing token", name)
	}
	env := []string{"PIPER_GIT_TOKEN=" + token}
	if user := firstNonEmpty(value.Data["username"], value.Data["user"]); user != "" {
		env = append(env, "PIPER_GIT_USER="+user)
	}
	return env, nil
}

func (s *Store) FindGitByRepo(ctx context.Context, projectID, repoURL string) (*Metadata, error) {
	all, err := s.repo.List(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var best *Metadata
	for _, m := range all {
		if m.Disabled || m.Kind != KindGit || m.Endpoint == "" {
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

func normalizeCreate(projectID string, req CreateRequest) (*Metadata, Value, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, Value{}, fmt.Errorf("name is required")
	}
	if req.Kind == "" {
		req.Kind = KindGeneric
	}
	if req.Kind != KindGeneric && req.Kind != KindGit && req.Kind != KindS3 {
		return nil, Value{}, fmt.Errorf("kind must be generic, git, or s3")
	}
	if err := validateEndpoint(req.Kind, req.Endpoint); err != nil {
		return nil, Value{}, err
	}
	data := cleanData(req.Data)
	if err := validateData(req.Kind, data); err != nil {
		return nil, Value{}, err
	}
	meta := &Metadata{
		ProjectID: projectID,
		Name:      name,
		Kind:      req.Kind,
		Endpoint:  normalizeEndpoint(req.Endpoint),
		Keys:      keysForKind(req.Kind, data),
	}
	return meta, Value{Data: data}, nil
}

func validateData(kind Kind, data map[string]string) error {
	if len(data) == 0 {
		return fmt.Errorf("data is required")
	}
	switch kind {
	case KindGeneric:
		return nil
	case KindGit:
		if firstNonEmpty(data["token"], data["password"]) == "" {
			return fmt.Errorf("git credential requires token or password")
		}
	case KindS3:
		if data["access_key_id"] == "" || data["secret_access_key"] == "" {
			return fmt.Errorf("s3 credential requires access_key_id and secret_access_key")
		}
	}
	return nil
}

func validateEndpoint(kind Kind, endpoint string) error {
	if endpoint == "" {
		return nil
	}
	if kind != KindGit {
		return fmt.Errorf("%s credential does not support endpoint", kind)
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("git endpoint must be a valid URL (e.g. https://github.com/myorg/)")
	}
	return nil
}

func normalizeEndpoint(endpoint string) string {
	return strings.TrimSpace(endpoint)
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

func keysForKind(kind Kind, data map[string]string) []string {
	if kind != KindGeneric {
		return nil
	}
	return keys(data)
}

func keys(data map[string]string) []string {
	out := make([]string, 0, len(data))
	for k := range data {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func checkScope(endpoint, repoURL string) error {
	if !inScope(endpoint, repoURL) {
		return fmt.Errorf("%w: %q not in scope %q", ErrScopeViolation, repoURL, endpoint)
	}
	return nil
}

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
	out = append(out, ciphertext...)
	return out, nil
}

func (s *Store) decrypt(encrypted []byte) (Value, error) {
	if len(encrypted) < 3+s.aead.NonceSize() || string(encrypted[:3]) != "v1:" {
		return Value{}, fmt.Errorf("invalid credential ciphertext")
	}
	body := encrypted[3:]
	nonce := body[:s.aead.NonceSize()]
	ciphertext := body[s.aead.NonceSize():]
	plain, err := s.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return Value{}, fmt.Errorf("decrypt credential: %w", err)
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
