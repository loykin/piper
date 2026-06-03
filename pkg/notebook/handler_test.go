package notebook

import (
	"net/http"
	"testing"

	"github.com/piper/piper/internal/tunnelproxy"
)

func TestNotebookProxyPolicyRewriteRequest(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/notebooks/demo/proxy/lab/?foo=bar", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://localhost:8080")
	policy, err := tunnelproxy.BuildPolicy("notebook", tunnelproxy.PolicyContext{
		Request:     req,
		Name:        "jj",
		Token:       "tok-123",
		Host:        "localhost:8080",
		Scheme:      "http",
		ProxyPrefix: "/notebooks/jj/proxy",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := policy.RewriteRequest(req); err != nil {
		t.Fatal(err)
	}
	if got, want := req.Header.Get("X-Forwarded-Host"), "localhost:8080"; got != want {
		t.Fatalf("X-Forwarded-Host = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("X-Forwarded-Proto"), "http"; got != want {
		t.Fatalf("X-Forwarded-Proto = %q, want %q", got, want)
	}
	if got := req.Header.Get("Origin"); got != "" {
		t.Fatalf("Origin = %q, want empty", got)
	}
	if got, want := req.URL.RawQuery, "foo=bar&token=tok-123"; got != want {
		t.Fatalf("query = %q, want %q", got, want)
	}
}

func TestRewriteNotebookRedirectLocation(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Location", "http://127.0.0.1:8888/lab?token=upstream")
	if err := tunnelproxy.RewriteLocationPrefix(resp, "/notebooks/jj/proxy", "token"); err != nil {
		t.Fatal(err)
	}
	if got, want := resp.Header.Get("Location"), "/notebooks/jj/proxy/lab"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}

	resp = &http.Response{Header: http.Header{}}
	resp.Header.Set("Location", "/lab")
	if err := tunnelproxy.RewriteLocationPrefix(resp, "/notebooks/jj/proxy", "token"); err != nil {
		t.Fatal(err)
	}
	if got, want := resp.Header.Get("Location"), "/notebooks/jj/proxy/lab"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

func TestRewriteNotebookRedirectLocationPreservesProxyPath(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Location", "http://127.0.0.1:8888/notebooks/jj/proxy/lab/?token=upstream")
	if err := tunnelproxy.RewriteLocationPrefix(resp, "/notebooks/jj/proxy", "token"); err != nil {
		t.Fatal(err)
	}
	if got, want := resp.Header.Get("Location"), "/notebooks/jj/proxy/lab/"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}
