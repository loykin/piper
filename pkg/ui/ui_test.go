package ui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerFallsBackForSPARoutes(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/notebooks", nil)

	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") == "application/json" {
		t.Fatal("SPA route returned API response")
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
