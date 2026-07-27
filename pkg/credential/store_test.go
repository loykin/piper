package credential

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/pkg/manifest"
	"github.com/piper/piper/pkg/project"
	"github.com/piper/piper/pkg/security"
)

const testProjectID = "project-1"

func TestStoreCreateListGetAndRotate(t *testing.T) {
	ctx := context.Background()
	store, repo := newTestStore(t)

	meta, err := store.Create(ctx, testProjectID, CreateRequest{
		Name: "app",
		Kind: KindGeneric,
		Data: map[string]string{"password": "old", "username": "alice"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if meta.ProjectID != testProjectID || meta.Name != "app" || meta.Kind != KindGeneric {
		t.Fatalf("metadata = %#v", meta)
	}
	if !reflect.DeepEqual(meta.Keys, []string{"password", "username"}) {
		t.Fatalf("keys = %#v", meta.Keys)
	}

	list, err := store.List(ctx, testProjectID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Name != "app" {
		t.Fatalf("List = %#v", list)
	}

	got, err := store.Get(ctx, testProjectID, "app")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || got.Name != "app" || !reflect.DeepEqual(got.Keys, []string{"password", "username"}) {
		t.Fatalf("Get = %#v", got)
	}

	if err := store.Rotate(ctx, testProjectID, "app", RotateRequest{Data: map[string]string{"password": "new"}}); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	value, err := store.Resolve(ctx, testProjectID, "app")
	if err != nil {
		t.Fatalf("Resolve after rotate: %v", err)
	}
	if value.Data["password"] != "new" {
		t.Fatalf("resolved password = %q", value.Data["password"])
	}
	if repo.markUsed[testProjectID+"/app"] != 1 {
		t.Fatalf("MarkUsed count = %d", repo.markUsed[testProjectID+"/app"])
	}
}

func TestStoreDeleteHardDeletesMetadataAndValue(t *testing.T) {
	ctx := context.Background()
	store, repo := newTestStore(t)
	if _, err := store.Create(ctx, testProjectID, CreateRequest{
		Name: "app",
		Kind: KindGeneric,
		Data: map[string]string{"token": "super-secret"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Delete(ctx, testProjectID, "app"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got, err := store.Get(ctx, testProjectID, "app"); err != nil || got != nil {
		t.Fatalf("Get after delete = %#v, %v", got, err)
	}
	if _, err := store.Resolve(ctx, testProjectID, "app"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve after delete error = %v, want %v", err, ErrNotFound)
	}
	if _, ok := repo.values[repoKey(testProjectID, "app")]; ok {
		t.Fatal("credential value remained after delete")
	}
	if err := store.Delete(ctx, testProjectID, "app"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete missing error = %v, want %v", err, ErrNotFound)
	}
}

func TestCredentialHandlerDoesNotExposeValues(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.Create(context.Background(), testProjectID, CreateRequest{
		Name: "app",
		Kind: KindGeneric,
		Data: map[string]string{"password": "super-secret"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	router := newCredentialTestRouter(store)

	for _, path := range []string{"/credentials", "/credentials/app"} {
		t.Run(path, func(t *testing.T) {
			rec := doCredentialRequest(router, http.MethodGet, path, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d: %s", path, rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if strings.Contains(body, "super-secret") || strings.Contains(body, "data") || strings.Contains(body, "value") {
				t.Fatalf("credential response exposed secret material: %s", body)
			}
		})
	}
}

func TestStorePatchEnableDisableAndEndpointValidation(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	if _, err := store.Create(ctx, testProjectID, CreateRequest{
		Name:     "git",
		Kind:     KindGit,
		Endpoint: "https://github.com/acme/",
		Data:     map[string]string{"token": "secret"},
	}); err != nil {
		t.Fatalf("Create git: %v", err)
	}

	disabled := false
	meta, err := store.Patch(ctx, testProjectID, "git", PatchRequest{Enabled: &disabled})
	if err != nil {
		t.Fatalf("Patch disable: %v", err)
	}
	if !meta.Disabled {
		t.Fatal("credential should be disabled")
	}
	if _, err := store.ResolveGit(ctx, testProjectID, "git", "https://github.com/acme/repo"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("ResolveGit disabled error = %v", err)
	}

	enabled := true
	endpoint := "https://github.com/acme/platform/"
	meta, err = store.Patch(ctx, testProjectID, "git", PatchRequest{Enabled: &enabled, Endpoint: &endpoint})
	if err != nil {
		t.Fatalf("Patch enable endpoint: %v", err)
	}
	if meta.Disabled || meta.Endpoint != endpoint {
		t.Fatalf("patched metadata = %#v", meta)
	}

	badEndpoint := "not a url"
	if _, err := store.Patch(ctx, testProjectID, "git", PatchRequest{Endpoint: &badEndpoint}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Patch invalid endpoint error = %v", err)
	}

	if _, err := store.Patch(ctx, testProjectID, "missing", PatchRequest{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Patch missing error = %v", err)
	}
}

func TestResolveEnv(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	if _, err := store.Create(ctx, testProjectID, CreateRequest{
		Name: "generic",
		Kind: KindGeneric,
		Data: map[string]string{"token": "abc"},
	}); err != nil {
		t.Fatalf("Create generic: %v", err)
	}
	if _, err := store.Create(ctx, testProjectID, CreateRequest{
		Name: "git",
		Kind: KindGit,
		Data: map[string]string{"token": "git-token"},
	}); err != nil {
		t.Fatalf("Create git: %v", err)
	}

	env, err := store.ResolveEnv(ctx, testProjectID, []manifest.EnvVar{
		{Name: "PLAIN", Value: "value"},
		{Name: "SECRET", ValueFrom: &manifest.EnvVarSource{CredentialRef: &manifest.CredentialRef{Name: "generic", Key: "token"}}},
	})
	if err != nil {
		t.Fatalf("ResolveEnv: %v", err)
	}
	if !reflect.DeepEqual(env, []string{"PLAIN=value", "SECRET=abc"}) {
		t.Fatalf("env = %#v", env)
	}

	cases := []struct {
		name string
		env  []manifest.EnvVar
		want error
	}{
		{
			name: "missing credential",
			env:  []manifest.EnvVar{{Name: "SECRET", ValueFrom: &manifest.EnvVarSource{CredentialRef: &manifest.CredentialRef{Name: "missing", Key: "token"}}}},
			want: ErrNotFound,
		},
		{
			name: "missing key",
			env:  []manifest.EnvVar{{Name: "SECRET", ValueFrom: &manifest.EnvVarSource{CredentialRef: &manifest.CredentialRef{Name: "generic", Key: "missing"}}}},
			want: nil,
		},
		{
			name: "wrong kind",
			env:  []manifest.EnvVar{{Name: "SECRET", ValueFrom: &manifest.EnvVarSource{CredentialRef: &manifest.CredentialRef{Name: "git", Key: "token"}}}},
			want: ErrInvalid,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.ResolveEnv(ctx, testProjectID, tc.env)
			if err == nil {
				t.Fatal("ResolveEnv succeeded, want error")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("ResolveEnv error = %v, want %v", err, tc.want)
			}
		})
	}

	disabled := false
	if _, err := store.Patch(ctx, testProjectID, "generic", PatchRequest{Enabled: &disabled}); err != nil {
		t.Fatalf("Patch disable: %v", err)
	}
	_, err = store.ResolveEnv(ctx, testProjectID, []manifest.EnvVar{{
		Name: "SECRET",
		ValueFrom: &manifest.EnvVarSource{CredentialRef: &manifest.CredentialRef{
			Name: "generic",
			Key:  "token",
		}},
	}})
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("ResolveEnv disabled error = %v", err)
	}
}

func TestGitEnvFindGitByRepoAndScope(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	for _, req := range []CreateRequest{
		{Name: "org", Kind: KindGit, Endpoint: "https://github.com/acme/", Data: map[string]string{"username": "u", "token": "org-token"}},
		{Name: "repo", Kind: KindGit, Endpoint: "https://github.com/acme/repo/", Data: map[string]string{"token": "repo-token"}},
		{Name: "other", Kind: KindGit, Endpoint: "https://github.com/acme-other/", Data: map[string]string{"token": "other-token"}},
	} {
		if _, err := store.Create(ctx, testProjectID, req); err != nil {
			t.Fatalf("Create %s: %v", req.Name, err)
		}
	}

	env, err := store.GitEnv(ctx, testProjectID, "org", "https://github.com/acme/project.git")
	if err != nil {
		t.Fatalf("GitEnv: %v", err)
	}
	if !reflect.DeepEqual(env, []string{"PIPER_GIT_TOKEN=org-token", "PIPER_GIT_USER=u"}) {
		t.Fatalf("GitEnv = %#v", env)
	}

	best, err := store.FindGitByRepo(ctx, testProjectID, "https://github.com/acme/repo/service.git")
	if err != nil {
		t.Fatalf("FindGitByRepo: %v", err)
	}
	if best == nil || best.Name != "repo" {
		t.Fatalf("best = %#v, want repo", best)
	}

	if _, err := store.ResolveGit(ctx, testProjectID, "org", "https://github.com/acme-evil/repo"); !errors.Is(err, ErrScopeViolation) {
		t.Fatalf("scope error = %v", err)
	}
}

func TestInjectGitCredentialsEncodesUserInfo(t *testing.T) {
	got := injectGitCredentials("https://github.com/acme/repo.git", "user@example.com", "tok@:/% space")
	want := "https://user%40example.com:tok%40%3A%2F%25%20space@github.com/acme/repo.git"
	if got != want {
		t.Fatalf("injectGitCredentials = %q, want %q", got, want)
	}
}

func TestScrubCredentialsMasksRawAndEncodedToken(t *testing.T) {
	token := "tok@:/% space"
	msg := "raw tok@:/% space encoded tok%40%3A%2F%25%20space"
	got := scrubCredentials(msg, token)
	if strings.Contains(got, token) || strings.Contains(got, escapedPassword(token)) {
		t.Fatalf("scrubbed message still contains token: %q", got)
	}
	if strings.Count(got, "***") != 2 {
		t.Fatalf("scrubbed message = %q", got)
	}
}

func newTestStore(t *testing.T) (*Store, *memoryRepo) {
	t.Helper()
	repo := &memoryRepo{
		meta:     map[string]*Metadata{},
		values:   map[string][]byte{},
		markUsed: map[string]int{},
	}
	store, err := NewStore(repo, "pbkdf2:test-key")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, repo
}

type memoryRepo struct {
	meta     map[string]*Metadata
	values   map[string][]byte
	markUsed map[string]int
}

func (r *memoryRepo) List(_ context.Context, projectID string) ([]*Metadata, error) {
	var out []*Metadata
	for _, meta := range r.meta {
		if meta.ProjectID == projectID {
			out = append(out, cloneMetadata(meta))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (r *memoryRepo) Get(_ context.Context, projectID, name string) (*Metadata, error) {
	meta := r.meta[repoKey(projectID, name)]
	if meta == nil {
		return nil, nil
	}
	return cloneMetadata(meta), nil
}

func (r *memoryRepo) Create(_ context.Context, meta *Metadata, encrypted []byte) error {
	key := repoKey(meta.ProjectID, meta.Name)
	if _, ok := r.meta[key]; ok {
		return ErrAlreadyExists
	}
	now := time.Now().UTC()
	clone := cloneMetadata(meta)
	clone.CreatedAt = now
	clone.UpdatedAt = now
	r.meta[key] = clone
	r.values[key] = append([]byte(nil), encrypted...)
	return nil
}

func (r *memoryRepo) Rotate(_ context.Context, projectID, name string, encrypted []byte, keys []string) error {
	key := repoKey(projectID, name)
	meta := r.meta[key]
	if meta == nil {
		return ErrNotFound
	}
	meta.Keys = append([]string(nil), keys...)
	meta.UpdatedAt = time.Now().UTC()
	r.values[key] = append([]byte(nil), encrypted...)
	return nil
}

func (r *memoryRepo) Patch(_ context.Context, projectID, name string, req PatchRequest) error {
	meta := r.meta[repoKey(projectID, name)]
	if meta == nil {
		return ErrNotFound
	}
	if req.Enabled != nil {
		meta.Disabled = !*req.Enabled
	}
	if req.Endpoint != nil {
		meta.Endpoint = *req.Endpoint
	}
	meta.UpdatedAt = time.Now().UTC()
	return nil
}

func (r *memoryRepo) Delete(_ context.Context, projectID, name string) error {
	key := repoKey(projectID, name)
	if r.meta[key] == nil {
		return ErrNotFound
	}
	delete(r.meta, key)
	delete(r.values, key)
	return nil
}

func (r *memoryRepo) GetValue(_ context.Context, projectID, name string) ([]byte, error) {
	value := r.values[repoKey(projectID, name)]
	if value == nil {
		return nil, ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (r *memoryRepo) MarkUsed(_ context.Context, projectID, name string) error {
	r.markUsed[repoKey(projectID, name)]++
	return nil
}

func (r *memoryRepo) RecordTestResult(_ context.Context, projectID, name string, ok bool, message string) error {
	meta := r.meta[repoKey(projectID, name)]
	if meta == nil {
		return ErrNotFound
	}
	now := time.Now().UTC()
	meta.LastTestedAt = &now
	meta.LastTestOK = &ok
	meta.LastTestMessage = message
	return nil
}

func repoKey(projectID, name string) string {
	return projectID + "/" + name
}

func cloneMetadata(meta *Metadata) *Metadata {
	if meta == nil {
		return nil
	}
	clone := *meta
	clone.Keys = append([]string(nil), meta.Keys...)
	if meta.LastUsedAt != nil {
		v := *meta.LastUsedAt
		clone.LastUsedAt = &v
	}
	if meta.LastTestedAt != nil {
		v := *meta.LastTestedAt
		clone.LastTestedAt = &v
	}
	if meta.LastTestOK != nil {
		v := *meta.LastTestOK
		clone.LastTestOK = &v
	}
	return &clone
}

func newCredentialTestRouter(store *Store) *gin.Engine {
	router := gin.New()
	injectProject := func(c *gin.Context) {
		ctx := project.WithContext(c.Request.Context(), project.Context{
			ID:   testProjectID,
			Role: security.ProjectRoleAdmin,
		})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
	NewHandler(store).RegisterRoutes(router.Group("", injectProject))
	return router
}

func doCredentialRequest(router *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}
