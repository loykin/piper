package credential

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"sort"
	"strings"

	"github.com/loykin/piper/pkg/manifest"
)

const (
	// credentialKDFSalt is fixed and public by design: this derives one
	// installation-wide encryption key from an operator-supplied passphrase,
	// not per-user password hashes, so there's no per-secret salt to protect —
	// only the passphrase's own strength matters. A fixed, application-specific
	// salt still stops precomputed-table reuse across unrelated applications.
	credentialKDFSalt = "piper-credential-store-v1"
	// credentialKDFIterations follows OWASP's 2023 minimum recommendation for
	// PBKDF2-HMAC-SHA256. This only runs once at process startup.
	credentialKDFIterations = 600_000
)

// InUseChecker reports whether (projectID, name) is still referenced by
// something outside this store's own records — e.g. the server's live or
// pending storage.CredentialRef — that Delete alone can't see, since this
// package is generic and deliberately has no knowledge of any specific
// consumer. A non-empty reason means "in use"; Delete refuses with
// ErrInUse wrapping it. Register via Store.AddInUseChecker.
type InUseChecker func(ctx context.Context, projectID, name string) (reason string, inUse bool)

type Store struct {
	repo          Repository
	aead          cipher.AEAD
	inUseCheckers []InUseChecker
}

// AddInUseChecker registers an additional guard Delete consults before
// removing a credential. Intended for the embedding application (e.g. Piper
// wiring a check against its own storage.CredentialRef) — this package
// itself never calls it.
func (s *Store) AddInUseChecker(c InUseChecker) {
	s.inUseCheckers = append(s.inUseCheckers, c)
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
	if strings.HasPrefix(key, "pbkdf2:") {
		passphrase := strings.TrimPrefix(key, "pbkdf2:")
		dk, err := pbkdf2.Key(sha256.New, passphrase, []byte(credentialKDFSalt), credentialKDFIterations, 32)
		if err != nil {
			return nil, fmt.Errorf("derive credential encryption key: %w", err)
		}
		return dk, nil
	}
	if b, err := base64.StdEncoding.DecodeString(key); err == nil && len(b) == 32 {
		return b, nil
	}
	if len(key) == 32 {
		return []byte(key), nil
	}
	return nil, fmt.Errorf("credential encryption key must be 32 bytes, base64-encoded 32 bytes, or pbkdf2:<passphrase>")
}

func (s *Store) List(ctx context.Context, projectID string, limit, offset int) ([]*Metadata, error) {
	return s.repo.List(ctx, projectID, limit, offset)
}

func (s *Store) Count(ctx context.Context, projectID string) (int, error) {
	return s.repo.Count(ctx, projectID)
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
	for _, check := range s.inUseCheckers {
		if reason, inUse := check(ctx, projectID, name); inUse {
			return fmt.Errorf("%w: %s", ErrInUse, reason)
		}
	}
	return s.repo.Delete(ctx, projectID, name)
}

func (s *Store) Resolve(ctx context.Context, projectID, name string) (Value, error) {
	return s.resolve(ctx, projectID, name, "", "")
}

func (s *Store) ResolveGit(ctx context.Context, projectID, name, repoURL string) (Value, error) {
	return s.resolve(ctx, projectID, name, string(KindGit), repoURL)
}

// ResolveS3 returns the decrypted values of an s3 credential (access_key_id,
// secret_access_key, and optional session_token).
func (s *Store) ResolveS3(ctx context.Context, projectID, name string) (Value, error) {
	return s.resolve(ctx, projectID, name, string(KindS3), "")
}

// ResolveGCS returns the decrypted values of a gcs credential
// (service_account_json).
func (s *Store) ResolveGCS(ctx context.Context, projectID, name string) (Value, error) {
	return s.resolve(ctx, projectID, name, string(KindGCS), "")
}

// ResolveAzure returns the decrypted values of an azure credential
// (account_name, account_key).
func (s *Store) ResolveAzure(ctx context.Context, projectID, name string) (Value, error) {
	return s.resolve(ctx, projectID, name, string(KindAzure), "")
}

// ResolveMlflow returns the decrypted values of an mlflow credential (token,
// or username/password, and optional ca_cert).
func (s *Store) ResolveMlflow(ctx context.Context, projectID, name string) (Value, error) {
	return s.resolve(ctx, projectID, name, string(KindMlflow), "")
}

func (s *Store) ValidateNotificationCredential(ctx context.Context, projectID, name string) error {
	meta, err := s.repo.Get(ctx, projectID, strings.TrimSpace(name))
	if err != nil {
		return err
	}
	if meta == nil {
		return ErrNotFound
	}
	if meta.Disabled {
		return ErrDisabled
	}
	if meta.Kind != KindSlack && meta.Kind != KindWebhook {
		return fmt.Errorf("%w: credential %q is kind %q, expected slack or webhook", ErrInvalid, name, meta.Kind)
	}
	return nil
}

// ValidateMlflowCredential checks that name refers to an existing, enabled,
// mlflow-kind credential before an MLflow integration is created/updated to
// reference it — same fetch/nil/disabled/kind-check shape as
// ValidateNotificationCredential, for the same reason (a credential_ref
// mistake should be caught at write time, not at export time).
func (s *Store) ValidateMlflowCredential(ctx context.Context, projectID, name string) error {
	meta, err := s.repo.Get(ctx, projectID, strings.TrimSpace(name))
	if err != nil {
		return err
	}
	if meta == nil {
		return ErrNotFound
	}
	if meta.Disabled {
		return ErrDisabled
	}
	if meta.Kind != KindMlflow {
		return fmt.Errorf("%w: credential %q is kind %q, expected mlflow", ErrInvalid, name, meta.Kind)
	}
	return nil
}

func (s *Store) ResolveNotification(ctx context.Context, projectID, name string) (NotificationCredential, error) {
	if err := s.ValidateNotificationCredential(ctx, projectID, name); err != nil {
		return NotificationCredential{}, err
	}
	meta, err := s.repo.Get(ctx, projectID, name)
	if err != nil || meta == nil {
		return NotificationCredential{}, err
	}
	value, err := s.resolve(ctx, projectID, name, string(meta.Kind), "")
	if err != nil {
		return NotificationCredential{}, err
	}
	return NotificationCredential{Kind: meta.Kind, Data: value.Data}, nil
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
	all, err := s.repo.List(ctx, projectID, 0, 0)
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
	if req.Kind != KindGeneric && req.Kind != KindGit && req.Kind != KindS3 && req.Kind != KindGCS && req.Kind != KindAzure && req.Kind != KindSlack && req.Kind != KindWebhook && req.Kind != KindMlflow {
		return nil, Value{}, fmt.Errorf("kind must be generic, git, s3, gcs, azure, slack, webhook, or mlflow")
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
	case KindGCS:
		if data["service_account_json"] == "" {
			return fmt.Errorf("gcs credential requires service_account_json")
		}
		var probe map[string]any
		if err := json.Unmarshal([]byte(data["service_account_json"]), &probe); err != nil {
			return fmt.Errorf("gcs credential service_account_json must be valid JSON: %w", err)
		}
	case KindAzure:
		if data["account_name"] == "" || data["account_key"] == "" {
			return fmt.Errorf("azure credential requires account_name and account_key")
		}
	case KindSlack:
		if data["webhook_url"] == "" {
			return fmt.Errorf("slack credential requires webhook_url")
		}
		if err := validateNotificationURL(data["webhook_url"]); err != nil {
			return fmt.Errorf("slack webhook_url: %w", err)
		}
		for key := range data {
			if key != "webhook_url" {
				return fmt.Errorf("slack credential field %q is not supported", key)
			}
		}
	case KindWebhook:
		if data["url"] == "" {
			return fmt.Errorf("webhook credential requires url")
		}
		if err := validateNotificationURL(data["url"]); err != nil {
			return fmt.Errorf("webhook url: %w", err)
		}
		for key := range data {
			if key != "url" && !strings.HasPrefix(key, "header_") {
				return fmt.Errorf("webhook credential field %q is not supported", key)
			}
		}
	case KindMlflow:
		hasToken := data["token"] != ""
		hasBasic := data["username"] != "" || data["password"] != ""
		switch {
		case !hasToken && !hasBasic:
			return fmt.Errorf("mlflow credential requires token or username/password")
		case hasToken && hasBasic:
			return fmt.Errorf("mlflow credential token is mutually exclusive with username/password")
		case hasBasic && (data["username"] == "" || data["password"] == ""):
			return fmt.Errorf("mlflow credential requires both username and password for HTTP Basic auth")
		}
		for key := range data {
			if key != "token" && key != "username" && key != "password" && key != "ca_cert" {
				return fmt.Errorf("mlflow credential field %q is not supported", key)
			}
		}
	}
	return nil
}

func validateNotificationURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("must be an https URL without userinfo")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("must not target a private or local address")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()) {
		return fmt.Errorf("must not target a private or local address")
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
