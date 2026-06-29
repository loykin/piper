package secret

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
		return nil, fmt.Errorf("secret repository is required")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("secret encryption key is required")
	}
	raw, err := decodeKey(key)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, fmt.Errorf("secret encryption key: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secret encryption: %w", err)
	}
	return &Store{repo: repo, aead: aead}, nil
}

func decodeKey(key string) ([]byte, error) {
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
	return nil, fmt.Errorf("secret encryption key must be 32 bytes, base64-encoded 32 bytes, or sha256:<passphrase>")
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
		return fmt.Errorf("%w: secret name is required", ErrInvalid)
	}
	if len(req.Data) == 0 {
		return fmt.Errorf("%w: secret data is required", ErrInvalid)
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
	value := Value{Data: cleanData(req.Data)}
	encrypted, err := s.encrypt(value)
	if err != nil {
		return err
	}
	return s.repo.Rotate(ctx, projectID, name, encrypted, keys(value.Data))
}

func (s *Store) Resolve(ctx context.Context, projectID, name string) (Value, error) {
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
	encrypted, err := s.repo.GetActiveValue(ctx, projectID, name)
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

func (s *Store) GitEnv(ctx context.Context, projectID, name string) ([]string, error) {
	value, err := s.Resolve(ctx, projectID, name)
	if err != nil {
		return nil, err
	}
	user := firstNonEmpty(value.Data["username"], value.Data["user"])
	token := firstNonEmpty(value.Data["token"], value.Data["password"])
	if token == "" {
		return nil, fmt.Errorf("secret %q missing token", name)
	}
	env := []string{"PIPER_GIT_TOKEN=" + token}
	if user != "" {
		env = append(env, "PIPER_GIT_USER="+user)
	}
	return env, nil
}

// ResolveEnv resolves env vars that reference secrets and returns them as
// "NAME=value" strings. Plain-value entries (ValueFrom == nil) are passed
// through as-is. Callers can pass the result directly into ExecSpec.Env.
func (s *Store) ResolveEnv(ctx context.Context, projectID string, env []manifest.EnvVar) ([]string, error) {
	out := make([]string, 0, len(env))
	for _, e := range env {
		if e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil {
			if e.Value != "" {
				out = append(out, e.Name+"="+e.Value)
			}
			continue
		}
		ref := e.ValueFrom.SecretKeyRef
		value, err := s.Resolve(ctx, projectID, ref.Name)
		if err != nil {
			return nil, fmt.Errorf("env %q: resolve secret %q: %w", e.Name, ref.Name, err)
		}
		v, ok := value.Data[ref.Key]
		if !ok {
			return nil, fmt.Errorf("env %q: secret %q has no key %q", e.Name, ref.Name, ref.Key)
		}
		out = append(out, e.Name+"="+v)
	}
	return out, nil
}

func normalizeCreate(projectID string, req CreateRequest) (*Metadata, Value, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, Value{}, fmt.Errorf("secret name is required")
	}
	if req.Type == "" {
		req.Type = TypeGit
	}
	if req.Provider == "" {
		req.Provider = ProviderPiperManaged
	}
	if req.Provider != ProviderPiperManaged {
		return nil, Value{}, fmt.Errorf("unsupported secret provider %q", req.Provider)
	}
	if len(req.Data) == 0 {
		return nil, Value{}, fmt.Errorf("secret data is required")
	}
	data := cleanData(req.Data)
	meta := &Metadata{
		ProjectID: projectID,
		Name:      name,
		Type:      req.Type,
		Provider:  req.Provider,
		Keys:      keys(data),
	}
	return meta, Value{Data: data}, nil
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

func keys(data map[string]string) []string {
	out := make([]string, 0, len(data))
	for k := range data {
		out = append(out, k)
	}
	sort.Strings(out)
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
	out = append(out, ciphertext...)
	return out, nil
}

func (s *Store) decrypt(encrypted []byte) (Value, error) {
	if len(encrypted) < 3+s.aead.NonceSize() || string(encrypted[:3]) != "v1:" {
		return Value{}, fmt.Errorf("invalid secret ciphertext")
	}
	body := encrypted[3:]
	nonce := body[:s.aead.NonceSize()]
	ciphertext := body[s.aead.NonceSize():]
	plain, err := s.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return Value{}, fmt.Errorf("decrypt secret: %w", err)
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

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
