// Package ui serves the React admin app embedded into the cmd/piper binary.
//
// It is deliberately internal/, not pkg/ — this is a UI for the official
// server binary, not a contract for library consumers to mount their own
// (see the AJ design write-up: pkg/ui used to be a public package with a
// README-documented mount contract, which committed this repo to keeping
// the SPA-routing/base-path/auth behavior stable for anyone importing it as
// a library, when in practice only cmd/piper ever mounted it — Prometheus's
// UI-is-a-binary-concern policy, not PocketBase's UI-is-a-framework-feature
// one).
//
// Handler() is built two different ways depending on the `builtinassets`
// build tag — see ui_embed.go and ui_stub.go. Everything actually shared
// between them (SPA routing, cache headers) lives here.
package ui

import (
	"io/fs"
	"net/http"
	"strings"
)

// newHandler serves the SPA out of sub when built is true; otherwise it
// always answers 503, explaining that this binary was built without the
// UI. Callers never construct an "empty but built" state — see
// ui_embed.go's own dist/assets check for why that distinction exists.
func newHandler(sub fs.FS, built bool) http.Handler {
	if !built {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "UI not built into this binary — build with 'make build' (which runs 'make ui' first), "+
				"or use the official release binary/container, which already include it", http.StatusServiceUnavailable)
		})
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve the file if it exists, otherwise fall back to SPA index.html
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(sub, path); err != nil {
			// SPA fallback
			w.Header().Set("Cache-Control", "no-cache")
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		if path == "index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fileServer.ServeHTTP(w, r)
	})
}
