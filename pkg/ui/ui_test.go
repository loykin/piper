package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerFallsBackForSPARoutes(t *testing.T) {
	for _, path := range []string{"/notebooks", "/notebooks/demo", "/pipelines/editor", "/storage"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)

		Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("path %q status = %d, want 200; body=%q", path, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Content-Type") == "application/json" {
			t.Fatalf("path %q returned API response", path)
		}
	}
}

func TestHandlerServesAssets(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}
}

func TestHandlerCachesVersionedAssets(t *testing.T) {
	entries, err := dist.ReadDir("dist/assets")
	if err != nil {
		t.Fatalf("read embedded assets: %v", err)
	}
	var assetPath string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".js") {
			assetPath = "/assets/" + entry.Name()
			break
		}
	}
	if assetPath == "" {
		t.Fatal("no embedded JavaScript asset found")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, assetPath, nil)

	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q, want immutable asset cache", got)
	}
}
