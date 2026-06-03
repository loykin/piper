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

// InjectQueryParamUnlessCookie appends a query parameter unless a cookie with
// the given prefix is already present. It returns the updated query string.
func InjectQueryParamUnlessCookie(req *http.Request, key, value, cookiePrefix string) string {
	if req == nil {
		return ""
	}
	rawQuery := req.URL.RawQuery
	if value == "" {
		return rawQuery
	}
	if cookiePrefix != "" {
		for _, c := range req.Cookies() {
			if strings.HasPrefix(c.Name, cookiePrefix) {
				return rawQuery
			}
		}
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
