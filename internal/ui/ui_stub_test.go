//go:build !builtinassets

package ui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandlerReturns503WithoutBuiltinAssets pins the default (no build tag)
// outcome: a plain `go build`/`go test` of this module must never require
// dist/ to exist, and must tell the operator how to get the real UI rather
// than 404ing or crashing. See ui_embed_test.go for the -tags builtinassets
// counterpart, which exercises the real embedded SPA.
func TestHandlerReturns503WithoutBuiltinAssets(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%q", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
}
