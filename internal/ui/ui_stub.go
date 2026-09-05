//go:build !builtinassets

package ui

import "net/http"

// Handler returns a placeholder that answers every request with 503 — the
// default outcome for `go build`/`go install` of this module, since the
// real UI (ui_embed.go, gated behind the builtinassets tag) needs dist/ to
// already contain a real `make ui` build at compile time, which a plain
// `go build` has no way to guarantee. This is intentional: piper is a
// library-usable Go module, and a library consumer's binary should not
// silently gain a multi-megabyte embedded SPA (or a build that breaks
// because dist/ doesn't exist) just from importing it. cmd/piper's own
// Makefile/CI build always pass -tags builtinassets after running `make
// ui` first — see Handler in ui_embed.go for the real implementation.
func Handler() http.Handler {
	return newHandler(nil, false)
}
