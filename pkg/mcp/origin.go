package mcp

import (
	"fmt"
	"net/http"
	"strings"
)

// OriginHostPolicy validates the Origin and Host headers of an inbound MCP
// HTTP request to defend against DNS rebinding (design doc §8.1: "Origin과
// Host allowlist를 검증하여 DNS rebinding을 막는다").
//
// The codebase has no pre-existing Origin/Host allowlist convention to
// reuse (checked: no "Origin" handling anywhere in config.go or the
// middleware package before this) — this is a new, minimal one scoped to
// the MCP transport only.
type OriginHostPolicy struct {
	// AllowedHosts, when non-empty, restricts the request's Host header to
	// an exact (case-insensitive) match against one of these values
	// (host[:port], matching net/http's r.Host format). Empty means "accept
	// any Host" — deliberately permissive rather than guessing at the
	// server's externally visible name from ServerConfig.Addr (which is
	// commonly just ":8080" with no host part and is frequently reached
	// through a reverse proxy under an entirely different hostname); an
	// operator enabling MCP for public exposure is expected to set this
	// explicitly, same as AllowedOrigins.
	AllowedHosts []string
	// AllowedOrigins is the exact (case-insensitive) allowlist for a
	// present, non-empty Origin header. A request with NO Origin header
	// always passes regardless of this list — design doc §8.1: "non-browser
	// MCP clients typically send no Origin", and requiring an allowlist
	// entry for the common case of a CLI/server MCP client would make the
	// feature unusable for its primary audience. A *browser*-originated
	// request (Origin present) must match exactly; there is no wildcard.
	AllowedOrigins []string
}

// Validate checks r.Host and r.Header.Get("Origin") against the policy.
func (p OriginHostPolicy) Validate(r *http.Request) error {
	if len(p.AllowedHosts) > 0 {
		host := r.Host
		ok := false
		for _, h := range p.AllowedHosts {
			if strings.EqualFold(strings.TrimSpace(h), host) {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("mcp: host %q is not allowed", host)
		}
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		return nil
	}
	for _, o := range p.AllowedOrigins {
		if strings.EqualFold(strings.TrimSpace(o), origin) {
			return nil
		}
	}
	return fmt.Errorf("mcp: origin %q is not allowed", origin)
}
