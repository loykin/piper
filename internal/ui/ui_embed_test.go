//go:build builtinassets

package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests require a real `make ui` build to have populated dist/
// before `go test -tags builtinassets ./internal/ui/...` runs — see the
// Makefile's `ui-test`/CI's "UI Embed Build" job, which is the only place
// that combination is exercised. Without a real dist/, Handler() falls
// back to the same "not built" 503 ui_stub_test.go checks, and every test
// below would fail on that fallback rather than the SPA behavior they're
// meant to pin.

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
