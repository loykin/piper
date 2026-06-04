package ui

import (
	"net/http"
	"net/http/httptest"
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
}
