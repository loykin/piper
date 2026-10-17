package tunnelproxy

import (
	"crypto/tls"
	"net/http"
	"testing"
)

func TestSetForwardedHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://example.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://localhost:8080")

	SetForwardedHeaders(req, "localhost:8080", "http")

	if got, want := req.Header.Get("X-Forwarded-Host"), "localhost:8080"; got != want {
		t.Fatalf("X-Forwarded-Host = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("X-Forwarded-Proto"), "http"; got != want {
		t.Fatalf("X-Forwarded-Proto = %q, want %q", got, want)
	}
	if got := req.Header.Get("Origin"); got != "" {
		t.Fatalf("Origin = %q, want empty", got)
	}
}

// A "username-*" cookie may come from another Jupyter on the same host; the
// token must still be injected.
func TestInjectQueryParamIgnoresForeignJupyterCookies(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://example.com/?foo=bar", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "username-localhost-18081", Value: "1"})
	if got := InjectQueryParam(req, "token", "tok-123"); got != "foo=bar&token=tok-123" {
		t.Fatalf("query = %q", got)
	}
}

func TestRequestSchemeAndPathHelpers(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://example.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := RequestScheme(req), "http"; got != want {
		t.Fatalf("RequestScheme(http) = %q, want %q", got, want)
	}
	req.TLS = &tls.ConnectionState{}
	if got, want := RequestScheme(req), "https"; got != want {
		t.Fatalf("RequestScheme(https) = %q, want %q", got, want)
	}
	if got, want := EnsureLeadingSlash("lab"), "/lab"; got != want {
		t.Fatalf("EnsureLeadingSlash = %q, want %q", got, want)
	}
	if got, want := JoinPathPrefix("/notebooks/jj/proxy", "lab"), "/notebooks/jj/proxy/lab"; got != want {
		t.Fatalf("JoinPathPrefix = %q, want %q", got, want)
	}
}
