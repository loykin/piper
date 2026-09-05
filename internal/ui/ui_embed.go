//go:build builtinassets

package ui

import (
	"embed"
	"io/fs"
	"net/http"
)

// dist is only embedded in a build that passes -tags builtinassets — see
// ui_stub.go for the default (no-UI) build and why this is opt-in rather
// than the default. `make ui` (Makefile) populates internal/ui/dist from
// the frontend build immediately before any such build; dist/ is git-
// ignored (internal/ui/dist/.gitignore) rather than committed, since a
// stale committed copy was exactly the source-control churn (a full
// hashed-asset diff on every frontend change, independent of any actual
// Go change) this design replaced.
//
//go:embed all:dist
var dist embed.FS

// Handler returns an http.Handler that serves the React SPA, or the same
// "not built" 503 as the non-builtinassets stub if dist/ exists (required
// for go:embed to compile) but wasn't actually populated by `make ui`
// before this particular build — e.g. a developer passing -tags
// builtinassets by hand without running `make ui` first.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic("ui: embed dist not found: " + err.Error())
	}
	if _, err := fs.Stat(sub, "assets"); err != nil {
		return newHandler(nil, false)
	}
	return newHandler(sub, true)
}
