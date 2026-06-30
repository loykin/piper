package secret

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/piper/piper/pkg/project"
	"github.com/piper/piper/pkg/security"
)

type memoryRepo struct {
	meta   map[string]*Metadata
	values map[string][]byte
}

func newMemoryRepo() *memoryRepo {
	return &memoryRepo{
		meta:   map[string]*Metadata{},
		values: map[string][]byte{},
	}
}

func (r *memoryRepo) List(context.Context, string) ([]*Metadata, error) {
	out := make([]*Metadata, 0, len(r.meta))
	for _, item := range r.meta {
		cp := *item
		out = append(out, &cp)
	}
	return out, nil
}

func (r *memoryRepo) Get(_ context.Context, projectID, name string) (*Metadata, error) {
	item := r.meta[projectID+"/"+name]
	if item == nil {
		return nil, nil
	}
	cp := *item
	return &cp, nil
}

func (r *memoryRepo) Create(_ context.Context, meta *Metadata, encrypted []byte) error {
	key := meta.ProjectID + "/" + meta.Name
	if r.meta[key] != nil && !r.meta[key].Disabled {
		return ErrAlreadyExists
	}
	now := time.Now().UTC()
	cp := *meta
	if existing := r.meta[key]; existing != nil {
		cp.CreatedAt = existing.CreatedAt
	} else {
		cp.CreatedAt = now
	}
	cp.UpdatedAt = now
	r.meta[key] = &cp
	r.values[key] = encrypted
	return nil
}

func (r *memoryRepo) Rotate(_ context.Context, projectID, name string, encrypted []byte, keys []string) error {
	key := projectID + "/" + name
	item := r.meta[key]
	if item == nil {
		return ErrNotFound
	}
	item.Keys = keys
	item.UpdatedAt = time.Now().UTC()
	r.values[key] = encrypted
	return nil
}

func (r *memoryRepo) SetEnabled(_ context.Context, projectID, name string, enabled bool) error {
	item := r.meta[projectID+"/"+name]
	if item == nil {
		return ErrNotFound
	}
	item.Disabled = !enabled
	item.UpdatedAt = time.Now().UTC()
	return nil
}

func (r *memoryRepo) Delete(_ context.Context, projectID, name string) error {
	return r.SetEnabled(context.Background(), projectID, name, false)
}

func (r *memoryRepo) GetActiveValue(_ context.Context, projectID, name string) ([]byte, error) {
	value := r.values[projectID+"/"+name]
	if value == nil {
		return nil, ErrNotFound
	}
	return value, nil
}

func (r *memoryRepo) MarkUsed(context.Context, string, string) error { return nil }

func newSecretTestRouter(t *testing.T, repo *memoryRepo) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store, err := NewStore(repo, "12345678901234567890123456789012")
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	injectProject := func(c *gin.Context) {
		ctx := project.WithContext(c.Request.Context(), project.Context{ID: "proj-1", Role: security.ProjectRoleAdmin})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
	NewHandler(repo, store).RegisterRoutes(router.Group("", injectProject))
	return router
}

func doSecretJSON(router *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
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

func TestCreateDuplicateReturnsConflict(t *testing.T) {
	repo := newMemoryRepo()
	router := newSecretTestRouter(t, repo)
	body := map[string]any{
		"name": "github",
		"data": map[string]string{"token": "tok"},
	}

	rec := doSecretJSON(router, http.MethodPost, "/secrets", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body)
	}
	rec = doSecretJSON(router, http.MethodPost, "/secrets", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want %d: %s", rec.Code, http.StatusConflict, rec.Body)
	}
}

func TestCreateReenablesDisabledSecretWithSameName(t *testing.T) {
	repo := newMemoryRepo()
	router := newSecretTestRouter(t, repo)
	first := map[string]any{
		"name": "github",
		"data": map[string]string{"token": "old"},
	}
	rec := doSecretJSON(router, http.MethodPost, "/secrets", first)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body)
	}
	rec = doSecretJSON(router, http.MethodDelete, "/secrets/github", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d: %s", rec.Code, rec.Body)
	}
	rec = doSecretJSON(router, http.MethodPost, "/secrets", map[string]any{
		"name": "github",
		"data": map[string]string{"token": "new"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("recreate status = %d, want %d: %s", rec.Code, http.StatusCreated, rec.Body)
	}
	if repo.meta["proj-1/github"].Disabled {
		t.Fatal("recreated secret is still disabled")
	}
}

func TestRotateMissingReturnsNotFound(t *testing.T) {
	router := newSecretTestRouter(t, newMemoryRepo())
	rec := doSecretJSON(router, http.MethodPost, "/secrets/missing/rotate", map[string]any{
		"data": map[string]string{"token": "tok"},
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusNotFound, rec.Body)
	}
}

func TestRotateDisabledReturnsConflict(t *testing.T) {
	repo := newMemoryRepo()
	router := newSecretTestRouter(t, repo)
	_ = doSecretJSON(router, http.MethodPost, "/secrets", map[string]any{
		"name": "github",
		"data": map[string]string{"token": "tok"},
	})
	_ = doSecretJSON(router, http.MethodDelete, "/secrets/github", nil)

	rec := doSecretJSON(router, http.MethodPost, "/secrets/github/rotate", map[string]any{
		"data": map[string]string{"token": "next"},
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusConflict, rec.Body)
	}
	if got := repo.meta["proj-1/github"].Disabled; !got {
		t.Fatal("rotate re-enabled a disabled secret")
	}
}

func TestPatchCanReenableDisabledSecret(t *testing.T) {
	repo := newMemoryRepo()
	router := newSecretTestRouter(t, repo)
	_ = doSecretJSON(router, http.MethodPost, "/secrets", map[string]any{
		"name": "github",
		"data": map[string]string{"token": "tok"},
	})
	_ = doSecretJSON(router, http.MethodDelete, "/secrets/github", nil)

	rec := doSecretJSON(router, http.MethodPatch, "/secrets/github", map[string]any{"enabled": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}
	if got := repo.meta["proj-1/github"].Disabled; got {
		t.Fatal("secret is still disabled")
	}
}

func TestDeleteMissingReturnsNotFound(t *testing.T) {
	router := newSecretTestRouter(t, newMemoryRepo())
	rec := doSecretJSON(router, http.MethodDelete, "/secrets/missing", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusNotFound, rec.Body)
	}
}
