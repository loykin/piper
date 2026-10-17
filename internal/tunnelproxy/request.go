package tunnelproxy

import (
	"net/http"
	"net/url"
	"strings"
)

// SetForwardedHeaders applies the browser-visible host/scheme to an upstream
// request and removes Origin so the upstream app keeps the proxy host.
func SetForwardedHeaders(req *http.Request, host, scheme string) {
	if req == nil {
		return
	}
	req.Header.Set("X-Forwarded-Host", host)
	req.Header.Set("X-Forwarded-Proto", scheme)
	req.Header.Del("Origin")
}

// InjectQueryParam sets a query parameter on every upstream request and
// returns the updated query string. It used to skip the parameter whenever any
// "username-*" cookie was present, but that cookie can belong to another
// Jupyter on the same host (the name carries host and port, the check only a
// prefix), and skipping left the user on Jupyter's own login page.
func InjectQueryParam(req *http.Request, key, value string) string {
	if req == nil {
		return ""
	}
	rawQuery := req.URL.RawQuery
	if value == "" {
		return rawQuery
	}
	values, _ := url.ParseQuery(rawQuery)
	if values == nil {
		values = url.Values{}
	}
	values.Set(key, value)
	return values.Encode()
}

// RequestScheme reports the external scheme for a request.
func RequestScheme(r *http.Request) string {
	if r != nil && r.TLS != nil {
		return "https"
	}
	return "http"
}

// EnsureLeadingSlash normalizes a path so it always starts with "/".
func EnsureLeadingSlash(path string) string {
	if path == "" || !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}

// JoinPathPrefix joins a base prefix with a sub-path while preserving a single
// slash boundary.
func JoinPathPrefix(prefix, subPath string) string {
	return strings.TrimRight(prefix, "/") + EnsureLeadingSlash(subPath)
}
